package notifications

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	ErrEndpointNotFound  = errors.New("notification endpoint was not found")
	ErrEndpointDisabled  = errors.New("notification endpoint is disabled")
	ErrTestDeliveryLimit = errors.New("notification test limit reached")
)

type routeSnapshot struct {
	channelID            string
	endpointID           string
	provider             string
	priority             int
	fallbackDelaySeconds int
	config               json.RawMessage
}

func AlertReference(correlationID, alertID string) string {
	if reference := strings.TrimSpace(correlationID); reference != "" {
		return reference
	}
	return strings.TrimSpace(alertID)
}

func AlertDedupeKey(alertReference, ruleID, endpointID string) string {
	return "alert:" + alertReference + ":" + ruleID + ":" + endpointID
}

func TestDedupeKey(endpointID, requestID string) string {
	return "test:" + endpointID + ":" + requestID
}

type TestDeliveryInput struct {
	RequestedByID string
	StoreID       string
	EndpointID    string
	RequestID     string
}

func EnqueueAlertTx(ctx context.Context, tx pgx.Tx, input AlertNotificationInput) ([]DeliverySummary, error) {
	payloadBytes, err := json.Marshal(BuildAlertPayload(input))
	if err != nil {
		return nil, err
	}
	ruleRows, err := tx.Query(ctx, `SELECT "id","isEnabled","minimumSeverity","alertTypes","cooldownSeconds" FROM "notification_rules" WHERE "storeId"=$1 ORDER BY "createdAt","id"`, input.StoreID)
	if err != nil {
		return nil, err
	}
	specs := []RuleSpec{}
	for ruleRows.Next() {
		var spec RuleSpec
		if err := ruleRows.Scan(&spec.ID, &spec.IsEnabled, &spec.MinimumSeverity, &spec.AlertTypes, &spec.CooldownSeconds); err != nil {
			ruleRows.Close()
			return nil, err
		}
		specs = append(specs, spec)
	}
	if err := ruleRows.Err(); err != nil {
		ruleRows.Close()
		return nil, err
	}
	ruleRows.Close()

	summaries := []DeliverySummary{}
	for _, rule := range MatchingRules(specs, input.Severity, input.AlertType) {
		suppressed, err := cooldownSuppresses(ctx, tx, rule, input)
		if err != nil {
			return nil, err
		}
		if suppressed {
			continue
		}
		routes, err := loadRoutes(ctx, tx, rule.ID, input.StoreID)
		if err != nil {
			return nil, err
		}
		if len(routes) == 0 {
			continue
		}
		minPriority := routes[0].priority
		alertReference := AlertReference(input.CorrelationID, input.AlertID)
		for _, route := range routes {
			status := StatusPending
			if route.priority != minPriority {
				status = StatusWaitingFallback
			}
			version := ResolveTemplateVersion(Provider(route.provider), route.config)
			dedupeKey := AlertDedupeKey(alertReference, rule.ID, route.endpointID)
			summary := DeliverySummary{
				ID:                   uuid.NewString(),
				Kind:                 DeliveryKindAlert,
				EndpointID:           route.endpointID,
				RuleID:               rule.ID,
				Provider:             route.provider,
				Priority:             route.priority,
				FallbackDelaySeconds: route.fallbackDelaySeconds,
				Status:               status,
				TemplateVersion:      version,
			}
			err := tx.QueryRow(ctx, `INSERT INTO "notification_deliveries" ("id","deliveryKind","storeId","alertId","ruleId","endpointId","ruleChannelId","dedupeKey","status","provider","priority","fallbackDelaySeconds","templateVersion","payload","maxAttempts","availableAt","updatedAt") VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,NOW(),NOW()) ON CONFLICT DO NOTHING RETURNING "id"`,
				summary.ID, DeliveryKindAlert, input.StoreID, input.AlertID, rule.ID, route.endpointID, route.channelID, dedupeKey, status, route.provider, route.priority, route.fallbackDelaySeconds, version, string(payloadBytes), MaxAttemptsDefault).Scan(&summary.ID)
			if err != nil {
				if err == pgx.ErrNoRows {
					continue
				}
				return nil, err
			}
			summaries = append(summaries, summary)
		}
	}
	return summaries, nil
}

func cooldownSuppresses(ctx context.Context, tx pgx.Tx, rule RuleSpec, input AlertNotificationInput) (bool, error) {
	correlationID := strings.TrimSpace(input.CorrelationID)
	if rule.CooldownSeconds <= 0 || correlationID == "" {
		return false, nil
	}
	// The correlation identifier is required so two independent emergencies at
	// the same camera are never collapsed merely because they occur close in time.
	fingerprint := input.StoreID + ":" + rule.ID + ":" + input.CameraID + ":" + input.AlertType + ":" + correlationID
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, fingerprint); err != nil {
		return false, err
	}
	var suppressed bool
	err := tx.QueryRow(ctx, `SELECT EXISTS (
  SELECT 1 FROM "notification_deliveries" d JOIN "alerts" a ON a."id"=d."alertId" AND a."storeId"=d."storeId"
  WHERE d."storeId"=$1 AND d."ruleId"=$2 AND a."id"<>$3 AND a."correlationId"=$4
    AND COALESCE(a."cameraId"::text,'')=$5 AND a."type"::text=$6
    AND d."createdAt">NOW()-make_interval(secs=>$7::double precision)
)`, input.StoreID, rule.ID, input.AlertID, correlationID, input.CameraID, input.AlertType, rule.CooldownSeconds).Scan(&suppressed)
	return suppressed, err
}

func loadRoutes(ctx context.Context, tx pgx.Tx, ruleID, storeID string) ([]routeSnapshot, error) {
	rows, err := tx.Query(ctx, `SELECT rc."id",rc."priority",rc."fallbackDelaySeconds",e."id",e."provider"::text,COALESCE(e."config",'{}'::jsonb) FROM "notification_rule_channels" rc JOIN "notification_endpoints" e ON e."id"=rc."endpointId" WHERE rc."ruleId"=$1 AND rc."storeId"=$2 AND rc."isEnabled" AND e."isEnabled" ORDER BY rc."priority",e."id"`, ruleID, storeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	routes := []routeSnapshot{}
	for rows.Next() {
		var route routeSnapshot
		if err := rows.Scan(&route.channelID, &route.priority, &route.fallbackDelaySeconds, &route.endpointID, &route.provider, &route.config); err != nil {
			return nil, err
		}
		routes = append(routes, route)
	}
	return routes, rows.Err()
}

func EnqueueTestTx(ctx context.Context, tx pgx.Tx, input TestDeliveryInput) (DeliverySummary, error) {
	// A transaction-scoped global lock serializes TEST admission, including
	// requests from multiple API instances. ALERT ingestion never takes this lock.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(587263910250905)`); err != nil {
		return DeliverySummary{}, err
	}
	var provider string
	var config json.RawMessage
	var enabled bool
	err := tx.QueryRow(ctx, `SELECT "provider"::text,"config","isEnabled" FROM "notification_endpoints" WHERE "id"=$1 AND "storeId"=$2`, input.EndpointID, input.StoreID).Scan(&provider, &config, &enabled)
	if err != nil {
		if err == pgx.ErrNoRows {
			return DeliverySummary{}, ErrEndpointNotFound
		}
		return DeliverySummary{}, err
	}
	if !enabled {
		return DeliverySummary{}, ErrEndpointDisabled
	}
	version := ResolveTemplateVersion(Provider(provider), config)
	payloadBytes, err := json.Marshal(BuildTestPayload(Provider(provider)))
	if err != nil {
		return DeliverySummary{}, err
	}
	summary := DeliverySummary{
		ID:              uuid.NewString(),
		Kind:            DeliveryKindTest,
		EndpointID:      input.EndpointID,
		Provider:        provider,
		Priority:        1,
		Status:          StatusPending,
		TemplateVersion: version,
	}
	dedupeKey := TestDedupeKey(input.EndpointID, input.RequestID)
	err = tx.QueryRow(ctx, `SELECT "id","status"::text FROM "notification_deliveries" WHERE "dedupeKey"=$1 AND "storeId"=$2`, dedupeKey, input.StoreID).Scan(&summary.ID, &summary.Status)
	if err == nil {
		return summary, nil
	}
	if err != pgx.ErrNoRows {
		return DeliverySummary{}, err
	}
	var limited bool
	err = tx.QueryRow(ctx, `SELECT
 count(*) FILTER (WHERE "endpointId"=$1 AND "createdAt">statement_timestamp()-interval '1 minute')>=1 OR
 count(*) FILTER (WHERE "endpointId"=$1 AND "createdAt">statement_timestamp()-interval '1 hour')>=3 OR
 count(*) FILTER (WHERE "storeId"=$2 AND "createdAt">statement_timestamp()-interval '1 hour')>=10 OR
 count(*) FILTER (WHERE "requestedById"=$3 AND "createdAt">statement_timestamp()-interval '1 hour')>=10 OR
 count(*) FILTER (WHERE "createdAt">statement_timestamp()-interval '1 hour')>=100 OR
 count(*) FILTER (WHERE "endpointId"=$1 AND "status" IN ('PENDING','PROCESSING','RETRY_SCHEDULED'))>=1 OR
 count(*) FILTER (WHERE "storeId"=$2 AND "status" IN ('PENDING','PROCESSING','RETRY_SCHEDULED'))>=3 OR
 count(*) FILTER (WHERE "status" IN ('PENDING','PROCESSING','RETRY_SCHEDULED'))>=50
 FROM "notification_deliveries" WHERE "deliveryKind"='TEST' AND ("createdAt">statement_timestamp()-interval '1 hour' OR "status" IN ('PENDING','PROCESSING','RETRY_SCHEDULED'))`, input.EndpointID, input.StoreID, nullableString(input.RequestedByID)).Scan(&limited)
	if err != nil {
		return DeliverySummary{}, err
	}
	if limited {
		return DeliverySummary{}, ErrTestDeliveryLimit
	}
	err = tx.QueryRow(ctx, `INSERT INTO "notification_deliveries" ("id","deliveryKind","storeId","alertId","ruleId","endpointId","ruleChannelId","dedupeKey","status","provider","priority","fallbackDelaySeconds","templateVersion","payload","maxAttempts","availableAt","updatedAt","requestedById","createdAt") VALUES ($1,$2,$3,NULL,NULL,$4,NULL,$5,$6,$7,$8,$9,$10,$11,$12,statement_timestamp(),statement_timestamp(),$13,statement_timestamp()) RETURNING "id","status"::text`,
		summary.ID, DeliveryKindTest, input.StoreID, input.EndpointID, dedupeKey, StatusPending, provider, 1, 0, version, string(payloadBytes), MaxAttemptsDefault, nullableString(input.RequestedByID)).Scan(&summary.ID, &summary.Status)
	return summary, err
}
