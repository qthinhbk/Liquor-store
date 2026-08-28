package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/liquor-store/security-api/internal/notifications"
)

const (
	maxDestinationRefRunes  = 200
	maxProviderAccountRunes = 200
	maxPriorityValue        = 999
)

type notificationEndpointResponse struct {
	ID                   string          `json:"id"`
	Provider             string          `json:"provider"`
	Label                string          `json:"label"`
	ProviderAccountRef   *string         `json:"providerAccountRef"`
	DestinationMasked    string          `json:"destinationMasked"`
	CredentialConfigured bool            `json:"credentialConfigured"`
	Config               json.RawMessage `json:"config"`
	IsEnabled            bool            `json:"isEnabled"`
	CreatedAt            time.Time       `json:"createdAt"`
	UpdatedAt            time.Time       `json:"updatedAt"`
}

type notificationRuleResponse struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	MinimumSeverity string    `json:"minimumSeverity"`
	AlertTypes      []string  `json:"alertTypes"`
	CooldownSeconds int       `json:"cooldownSeconds"`
	IsEnabled       bool      `json:"isEnabled"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

type notificationRuleChannelResponse struct {
	ID                   string    `json:"id"`
	RuleID               string    `json:"ruleId"`
	EndpointID           string    `json:"endpointId"`
	EndpointProvider     string    `json:"endpointProvider"`
	EndpointLabel        string    `json:"endpointLabel"`
	Priority             int       `json:"priority"`
	FallbackDelaySeconds int       `json:"fallbackDelaySeconds"`
	IsEnabled            bool      `json:"isEnabled"`
	CreatedAt            time.Time `json:"createdAt"`
	UpdatedAt            time.Time `json:"updatedAt"`
}

type notificationEndpointInput struct {
	Provider           *string         `json:"provider"`
	Label              *string         `json:"label"`
	ProviderAccountRef *string         `json:"providerAccountRef"`
	DestinationRef     *string         `json:"destinationRef"`
	CredentialRef      *string         `json:"credentialRef"`
	Config             json.RawMessage `json:"config"`
	IsEnabled          *bool           `json:"isEnabled"`
}

func (input notificationEndpointInput) provided() bool {
	return input.Provider != nil || input.Label != nil || input.ProviderAccountRef != nil ||
		input.DestinationRef != nil || input.CredentialRef != nil || input.Config != nil || input.IsEnabled != nil
}

type notificationEndpointUpdateInput struct {
	Label              *string         `json:"label"`
	ProviderAccountRef *string         `json:"providerAccountRef"`
	DestinationRef     *string         `json:"destinationRef"`
	CredentialRef      *string         `json:"credentialRef"`
	Config             json.RawMessage `json:"config"`
	IsEnabled          *bool           `json:"isEnabled"`
}

func (input notificationEndpointUpdateInput) provided() bool {
	return input.Label != nil || input.ProviderAccountRef != nil || input.DestinationRef != nil ||
		input.CredentialRef != nil || input.Config != nil || input.IsEnabled != nil
}

type notificationRuleInput struct {
	Name            *string   `json:"name"`
	MinimumSeverity *string   `json:"minimumSeverity"`
	AlertTypes      *[]string `json:"alertTypes"`
	CooldownSeconds *int      `json:"cooldownSeconds"`
	IsEnabled       *bool     `json:"isEnabled"`
}

func (input notificationRuleInput) provided() bool {
	return input.Name != nil || input.MinimumSeverity != nil || input.AlertTypes != nil || input.CooldownSeconds != nil || input.IsEnabled != nil
}

type notificationRuleChannelInput struct {
	EndpointID           *string `json:"endpointId"`
	Priority             *int    `json:"priority"`
	FallbackDelaySeconds *int    `json:"fallbackDelaySeconds"`
	IsEnabled            *bool   `json:"isEnabled"`
}

func (input notificationRuleChannelInput) provided() bool {
	return input.EndpointID != nil || input.Priority != nil || input.FallbackDelaySeconds != nil || input.IsEnabled != nil
}

type notificationRuleChannelUpdateInput struct {
	Priority             *int  `json:"priority"`
	FallbackDelaySeconds *int  `json:"fallbackDelaySeconds"`
	IsEnabled            *bool `json:"isEnabled"`
}

func (input notificationRuleChannelUpdateInput) provided() bool {
	return input.Priority != nil || input.FallbackDelaySeconds != nil || input.IsEnabled != nil
}

func requireUUIDPath(w http.ResponseWriter, values ...string) bool {
	for _, value := range values {
		if _, err := uuid.Parse(value); err != nil {
			writeError(w, http.StatusNotFound, "Not Found", "Resource was not found.")
			return false
		}
	}
	return true
}

func writeValidationError(w http.ResponseWriter, err error) {
	writeError(w, http.StatusBadRequest, "Bad Request", err.Error())
}

func optionalTrimmed(value *string) any {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return trimmed
}

func derefTrimmed(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func validateEndpointTextFields(label, providerAccountRef, destinationRef, credentialRef *string) error {
	if label != nil && !validName(*label) {
		return errors.New("label must contain 1 to 120 characters.")
	}
	if providerAccountRef != nil && utf8.RuneCountInString(strings.TrimSpace(*providerAccountRef)) > maxProviderAccountRunes {
		return errors.New("providerAccountRef must be at most 200 characters.")
	}
	if destinationRef != nil {
		trimmed := strings.TrimSpace(*destinationRef)
		if trimmed == "" || utf8.RuneCountInString(trimmed) > maxDestinationRefRunes {
			return errors.New("destinationRef must contain 1 to 200 characters.")
		}
	}
	if credentialRef != nil && !notifications.ValidCredentialRef(*credentialRef) {
		return errors.New("credentialRef must reference the secret manager with env:// or render-secret:// and must never contain a raw credential.")
	}
	return nil
}

func validateEndpointEnableGate(provider, providerAccountRef, destinationRef string, enabled bool, config json.RawMessage) error {
	if provider != string(notifications.ProviderWhatsApp) || !enabled {
		return nil
	}
	return notifications.ValidateWhatsAppEnableConfig(providerAccountRef, destinationRef, config)
}

func scanNotificationEndpoint(row rowScanner, item *notificationEndpointResponse) error {
	var destinationRef, credentialRef string
	var config []byte
	if err := row.Scan(&item.ID, &item.Provider, &item.Label, &item.ProviderAccountRef, &destinationRef, &credentialRef, &config, &item.IsEnabled, &item.CreatedAt, &item.UpdatedAt); err != nil {
		return err
	}
	item.DestinationMasked = notifications.MaskDestination(destinationRef)
	item.CredentialConfigured = strings.TrimSpace(credentialRef) != ""
	item.Config = notifications.SanitizeConfigForResponse(config)
	return nil
}

func (s *Server) listNotificationEndpoints(w http.ResponseWriter, r *http.Request, user principal) {
	storeID := r.PathValue("storeId")
	if !requireUUIDPath(w, storeID) || !s.requireRole(w, r.Context(), user.ID, storeID, "OWNER") {
		return
	}
	rows, err := s.db.Query(r.Context(), `SELECT "id","provider"::text,"label","providerAccountRef","destinationRef","credentialRef","config","isEnabled","createdAt","updatedAt" FROM "notification_endpoints" WHERE "storeId"=$1 ORDER BY "createdAt","id"`, storeID)
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer rows.Close()
	items := []notificationEndpointResponse{}
	for rows.Next() {
		var item notificationEndpointResponse
		if err := scanNotificationEndpoint(rows, &item); err != nil {
			s.internalError(w, err)
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) createNotificationEndpoint(w http.ResponseWriter, r *http.Request, user principal) {
	storeID := r.PathValue("storeId")
	if !requireUUIDPath(w, storeID) || !s.requireRole(w, r.Context(), user.ID, storeID, "OWNER") {
		return
	}
	var input notificationEndpointInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Provider == nil || input.Label == nil || input.DestinationRef == nil || input.CredentialRef == nil {
		writeError(w, http.StatusBadRequest, "Bad Request", "provider, label, destinationRef and credentialRef are required.")
		return
	}
	provider := strings.TrimSpace(*input.Provider)
	if !oneOf(provider, string(notifications.ProviderTelegram), string(notifications.ProviderWhatsApp)) {
		writeError(w, http.StatusBadRequest, "Bad Request", "provider must be TELEGRAM or WHATSAPP.")
		return
	}
	isEnabled := true
	if input.IsEnabled != nil {
		isEnabled = *input.IsEnabled
	}
	configRaw := json.RawMessage("{}")
	if input.Config != nil {
		configRaw = input.Config
	}
	if err := validateEndpointTextFields(input.Label, input.ProviderAccountRef, input.DestinationRef, input.CredentialRef); err != nil {
		writeValidationError(w, err)
		return
	}
	if err := notifications.ValidateConfigObject(configRaw); err != nil {
		writeValidationError(w, err)
		return
	}
	if err := validateEndpointEnableGate(provider, derefTrimmed(input.ProviderAccountRef), strings.TrimSpace(*input.DestinationRef), isEnabled, configRaw); err != nil {
		writeValidationError(w, err)
		return
	}
	endpointID := uuid.NewString()
	_, err := s.db.Exec(r.Context(), `INSERT INTO "notification_endpoints" ("id","storeId","provider","label","providerAccountRef","destinationRef","credentialRef","config","isEnabled","updatedAt") VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,NOW())`,
		endpointID, storeID, provider, strings.TrimSpace(*input.Label), optionalTrimmed(input.ProviderAccountRef), strings.TrimSpace(*input.DestinationRef), strings.TrimSpace(*input.CredentialRef), configRaw, isEnabled)
	if err != nil {
		if uniqueViolation(err) {
			writeError(w, http.StatusConflict, "Conflict", "This destination is already configured for this provider in the store.")
			return
		}
		s.internalError(w, err)
		return
	}
	writeNotificationEndpoint(w, r.Context(), s, storeID, endpointID, http.StatusCreated)
}

func writeNotificationEndpoint(w http.ResponseWriter, ctx context.Context, s *Server, storeID, endpointID string, status int) {
	var item notificationEndpointResponse
	row := s.db.QueryRow(ctx, `SELECT "id","provider"::text,"label","providerAccountRef","destinationRef","credentialRef","config","isEnabled","createdAt","updatedAt" FROM "notification_endpoints" WHERE "id"=$1 AND "storeId"=$2`, endpointID, storeID)
	if err := scanNotificationEndpoint(row, &item); err != nil {
		if err == pgx.ErrNoRows {
			writeError(w, http.StatusNotFound, "Not Found", "Notification endpoint was not found.")
			return
		}
		s.internalError(w, err)
		return
	}
	writeJSON(w, status, item)
}

func (s *Server) updateNotificationEndpoint(w http.ResponseWriter, r *http.Request, user principal) {
	storeID, endpointID := r.PathValue("storeId"), r.PathValue("endpointId")
	if !requireUUIDPath(w, storeID, endpointID) || !s.requireRole(w, r.Context(), user.ID, storeID, "OWNER") {
		return
	}
	var input notificationEndpointUpdateInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if !input.provided() {
		writeError(w, http.StatusBadRequest, "Bad Request", "Request body must contain at least one updatable field.")
		return
	}
	if err := validateEndpointTextFields(input.Label, input.ProviderAccountRef, input.DestinationRef, input.CredentialRef); err != nil {
		writeValidationError(w, err)
		return
	}
	var current struct {
		Provider           string
		Label              string
		ProviderAccountRef *string
		DestinationRef     string
		CredentialRef      string
		Config             []byte
		IsEnabled          bool
	}
	err := s.db.QueryRow(r.Context(), `SELECT "provider"::text,"label","providerAccountRef","destinationRef","credentialRef","config","isEnabled" FROM "notification_endpoints" WHERE "id"=$1 AND "storeId"=$2`, endpointID, storeID).
		Scan(&current.Provider, &current.Label, &current.ProviderAccountRef, &current.DestinationRef, &current.CredentialRef, &current.Config, &current.IsEnabled)
	if err != nil {
		if err == pgx.ErrNoRows {
			writeError(w, http.StatusNotFound, "Not Found", "Notification endpoint was not found.")
			return
		}
		s.internalError(w, err)
		return
	}
	label := current.Label
	if input.Label != nil {
		label = strings.TrimSpace(*input.Label)
	}
	accountRef := current.ProviderAccountRef
	if input.ProviderAccountRef != nil {
		trimmed := strings.TrimSpace(*input.ProviderAccountRef)
		accountRef = nil
		if trimmed != "" {
			accountRef = &trimmed
		}
	}
	destinationRef := current.DestinationRef
	if input.DestinationRef != nil {
		destinationRef = strings.TrimSpace(*input.DestinationRef)
	}
	credentialRef := current.CredentialRef
	if input.CredentialRef != nil {
		credentialRef = strings.TrimSpace(*input.CredentialRef)
	}
	configRaw := json.RawMessage(current.Config)
	if input.Config != nil {
		configRaw = input.Config
	}
	isEnabled := current.IsEnabled
	if input.IsEnabled != nil {
		isEnabled = *input.IsEnabled
	}
	if err := validateEndpointTextFields(&label, nil, &destinationRef, &credentialRef); err != nil {
		writeValidationError(w, err)
		return
	}
	if err := notifications.ValidateConfigObject(configRaw); err != nil {
		writeValidationError(w, err)
		return
	}
	if err := validateEndpointEnableGate(current.Provider, derefString(accountRef), destinationRef, isEnabled, configRaw); err != nil {
		writeValidationError(w, err)
		return
	}
	var item notificationEndpointResponse
	row := s.db.QueryRow(r.Context(), `UPDATE "notification_endpoints" SET "label"=$3,"providerAccountRef"=$4,"destinationRef"=$5,"credentialRef"=$6,"config"=$7,"isEnabled"=$8,"updatedAt"=NOW() WHERE "id"=$1 AND "storeId"=$2 RETURNING "id","provider"::text,"label","providerAccountRef","destinationRef","credentialRef","config","isEnabled","createdAt","updatedAt"`,
		endpointID, storeID, label, accountRef, destinationRef, credentialRef, configRaw, isEnabled)
	if err := scanNotificationEndpoint(row, &item); err != nil {
		if uniqueViolation(err) {
			writeError(w, http.StatusConflict, "Conflict", "This destination is already configured for this provider in the store.")
			return
		}
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func (s *Server) removeNotificationEndpoint(w http.ResponseWriter, r *http.Request, user principal) {
	storeID, endpointID := r.PathValue("storeId"), r.PathValue("endpointId")
	if !requireUUIDPath(w, storeID, endpointID) || !s.requireRole(w, r.Context(), user.ID, storeID, "OWNER") {
		return
	}
	var referenced bool
	if err := s.db.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM "notification_deliveries" WHERE "endpointId"=$1 AND "storeId"=$2)`, endpointID, storeID).Scan(&referenced); err != nil {
		s.internalError(w, err)
		return
	}
	if referenced {
		writeError(w, http.StatusConflict, "Conflict", "This endpoint has delivery history. Disable it with PATCH isEnabled=false instead of deleting it.")
		return
	}
	result, err := s.db.Exec(r.Context(), `DELETE FROM "notification_endpoints" WHERE "id"=$1 AND "storeId"=$2`, endpointID, storeID)
	if err != nil {
		if foreignKeyViolation(err) {
			writeError(w, http.StatusConflict, "Conflict", "This endpoint has delivery history. Disable it with PATCH isEnabled=false instead of deleting it.")
			return
		}
		s.internalError(w, err)
		return
	}
	if result.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "Not Found", "Notification endpoint was not found.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) testNotificationEndpoint(w http.ResponseWriter, r *http.Request, user principal) {
	storeID, endpointID := r.PathValue("storeId"), r.PathValue("endpointId")
	if !requireUUIDPath(w, storeID, endpointID) || !s.requireRole(w, r.Context(), user.ID, storeID, "OWNER") {
		return
	}
	var input struct {
		RequestID string `json:"requestId"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	requestID := strings.TrimSpace(input.RequestID)
	if _, err := uuid.Parse(requestID); err != nil {
		writeError(w, http.StatusBadRequest, "Bad Request", "requestId is required and must be a UUID.")
		return
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	summary, err := notifications.EnqueueTestTx(r.Context(), tx, notifications.TestDeliveryInput{StoreID: storeID, EndpointID: endpointID, RequestID: requestID})
	if err != nil {
		switch {
		case errors.Is(err, notifications.ErrEndpointNotFound):
			writeError(w, http.StatusNotFound, "Not Found", "Notification endpoint was not found.")
		case errors.Is(err, notifications.ErrEndpointDisabled):
			writeError(w, http.StatusConflict, "Conflict", "Enable the endpoint before requesting a test delivery.")
		default:
			s.internalError(w, err)
		}
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"id": summary.ID, "status": summary.Status, "deliveryKind": summary.Kind})
}

func normalizeAlertTypes(values []string) ([]string, string) {
	normalized := []string{}
	seen := map[string]bool{}
	for _, value := range values {
		candidate := strings.ToUpper(strings.TrimSpace(value))
		if !oneOf(candidate, alertTypeValues...) {
			return nil, "alertTypes contains an unknown alert type."
		}
		if !seen[candidate] {
			seen[candidate] = true
			normalized = append(normalized, candidate)
		}
	}
	return normalized, ""
}

func validName(value string) bool { return validLength(value, 1, 120) }

type resolvedRule struct {
	Name            string
	MinimumSeverity string
	AlertTypes      []string
	CooldownSeconds int
	IsEnabled       bool
}

func resolveRuleCreate(input notificationRuleInput) (resolvedRule, error) {
	name := derefTrimmed(input.Name)
	if !validName(name) {
		return resolvedRule{}, errors.New("name must contain 1 to 120 characters.")
	}
	minimumSeverity := "CRITICAL"
	if input.MinimumSeverity != nil {
		minimumSeverity = strings.ToUpper(strings.TrimSpace(*input.MinimumSeverity))
	}
	alertTypes := []string{}
	if input.AlertTypes != nil {
		normalized, message := normalizeAlertTypes(*input.AlertTypes)
		if message != "" {
			return resolvedRule{}, errors.New(message)
		}
		alertTypes = normalized
	}
	cooldownSeconds := 0
	if input.CooldownSeconds != nil {
		cooldownSeconds = *input.CooldownSeconds
	}
	isEnabled := true
	if input.IsEnabled != nil {
		isEnabled = *input.IsEnabled
	}
	resolved := resolvedRule{Name: name, MinimumSeverity: minimumSeverity, AlertTypes: alertTypes, CooldownSeconds: cooldownSeconds, IsEnabled: isEnabled}
	if err := validateResolvedRule(resolved); err != nil {
		return resolvedRule{}, err
	}
	return resolved, nil
}

func applyRuleUpdate(current resolvedRule, input notificationRuleInput) (resolvedRule, error) {
	merged := current
	merged.AlertTypes = append([]string{}, current.AlertTypes...)
	if input.Name != nil {
		merged.Name = strings.TrimSpace(*input.Name)
	}
	if input.MinimumSeverity != nil {
		merged.MinimumSeverity = strings.ToUpper(strings.TrimSpace(*input.MinimumSeverity))
	}
	if input.AlertTypes != nil {
		normalized, message := normalizeAlertTypes(*input.AlertTypes)
		if message != "" {
			return resolvedRule{}, errors.New(message)
		}
		merged.AlertTypes = normalized
	}
	if input.CooldownSeconds != nil {
		merged.CooldownSeconds = *input.CooldownSeconds
	}
	if input.IsEnabled != nil {
		merged.IsEnabled = *input.IsEnabled
	}
	if err := validateResolvedRule(merged); err != nil {
		return resolvedRule{}, err
	}
	return merged, nil
}

func validateResolvedRule(rule resolvedRule) error {
	if !validName(rule.Name) {
		return errors.New("name must contain 1 to 120 characters.")
	}
	if !notifications.ValidSeverity(rule.MinimumSeverity) {
		return errors.New("minimumSeverity must be LOW, MEDIUM, HIGH or CRITICAL.")
	}
	if rule.CooldownSeconds < 0 || rule.CooldownSeconds > 86400 {
		return errors.New("cooldownSeconds must be between 0 and 86400.")
	}
	return nil
}

func scanNotificationRule(row rowScanner, item *notificationRuleResponse) error {
	if err := row.Scan(&item.ID, &item.Name, &item.MinimumSeverity, &item.AlertTypes, &item.CooldownSeconds, &item.IsEnabled, &item.CreatedAt, &item.UpdatedAt); err != nil {
		return err
	}
	if item.AlertTypes == nil {
		item.AlertTypes = []string{}
	}
	return nil
}

func (s *Server) listNotificationRules(w http.ResponseWriter, r *http.Request, user principal) {
	storeID := r.PathValue("storeId")
	if !requireUUIDPath(w, storeID) || !s.requireRole(w, r.Context(), user.ID, storeID, "OWNER") {
		return
	}
	rows, err := s.db.Query(r.Context(), `SELECT "id","name","minimumSeverity"::text,"alertTypes","cooldownSeconds","isEnabled","createdAt","updatedAt" FROM "notification_rules" WHERE "storeId"=$1 ORDER BY "createdAt","id"`, storeID)
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer rows.Close()
	items := []notificationRuleResponse{}
	for rows.Next() {
		var item notificationRuleResponse
		if err := scanNotificationRule(rows, &item); err != nil {
			s.internalError(w, err)
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) createNotificationRule(w http.ResponseWriter, r *http.Request, user principal) {
	storeID := r.PathValue("storeId")
	if !requireUUIDPath(w, storeID) || !s.requireRole(w, r.Context(), user.ID, storeID, "OWNER") {
		return
	}
	var input notificationRuleInput
	if !decodeJSON(w, r, &input) {
		return
	}
	resolved, err := resolveRuleCreate(input)
	if err != nil {
		writeValidationError(w, err)
		return
	}
	var item notificationRuleResponse
	if err := s.db.QueryRow(r.Context(), `INSERT INTO "notification_rules" ("id","storeId","name","minimumSeverity","alertTypes","cooldownSeconds","isEnabled","updatedAt") VALUES ($1,$2,$3,$4,$5,$6,$7,NOW()) RETURNING "id","name","minimumSeverity"::text,"alertTypes","cooldownSeconds","isEnabled","createdAt","updatedAt"`,
		uuid.NewString(), storeID, resolved.Name, resolved.MinimumSeverity, resolved.AlertTypes, resolved.CooldownSeconds, resolved.IsEnabled).Scan(&item.ID, &item.Name, &item.MinimumSeverity, &item.AlertTypes, &item.CooldownSeconds, &item.IsEnabled, &item.CreatedAt, &item.UpdatedAt); err != nil {
		s.internalError(w, err)
		return
	}
	if item.AlertTypes == nil {
		item.AlertTypes = []string{}
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) updateNotificationRule(w http.ResponseWriter, r *http.Request, user principal) {
	storeID, ruleID := r.PathValue("storeId"), r.PathValue("ruleId")
	if !requireUUIDPath(w, storeID, ruleID) || !s.requireRole(w, r.Context(), user.ID, storeID, "OWNER") {
		return
	}
	var input notificationRuleInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if !input.provided() {
		writeError(w, http.StatusBadRequest, "Bad Request", "Request body must contain at least one updatable field.")
		return
	}
	var current resolvedRule
	err := s.db.QueryRow(r.Context(), `SELECT "name","minimumSeverity"::text,"alertTypes","cooldownSeconds","isEnabled" FROM "notification_rules" WHERE "id"=$1 AND "storeId"=$2`, ruleID, storeID).
		Scan(&current.Name, &current.MinimumSeverity, &current.AlertTypes, &current.CooldownSeconds, &current.IsEnabled)
	if err != nil {
		if err == pgx.ErrNoRows {
			writeError(w, http.StatusNotFound, "Not Found", "Notification rule was not found.")
			return
		}
		s.internalError(w, err)
		return
	}
	resolved, err := applyRuleUpdate(current, input)
	if err != nil {
		writeValidationError(w, err)
		return
	}
	var item notificationRuleResponse
	if err := s.db.QueryRow(r.Context(), `UPDATE "notification_rules" SET "name"=$3,"minimumSeverity"=$4,"alertTypes"=$5,"cooldownSeconds"=$6,"isEnabled"=$7,"updatedAt"=NOW() WHERE "id"=$1 AND "storeId"=$2 RETURNING "id","name","minimumSeverity"::text,"alertTypes","cooldownSeconds","isEnabled","createdAt","updatedAt"`,
		ruleID, storeID, resolved.Name, resolved.MinimumSeverity, resolved.AlertTypes, resolved.CooldownSeconds, resolved.IsEnabled).Scan(&item.ID, &item.Name, &item.MinimumSeverity, &item.AlertTypes, &item.CooldownSeconds, &item.IsEnabled, &item.CreatedAt, &item.UpdatedAt); err != nil {
		s.internalError(w, err)
		return
	}
	if item.AlertTypes == nil {
		item.AlertTypes = []string{}
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) removeNotificationRule(w http.ResponseWriter, r *http.Request, user principal) {
	storeID, ruleID := r.PathValue("storeId"), r.PathValue("ruleId")
	if !requireUUIDPath(w, storeID, ruleID) || !s.requireRole(w, r.Context(), user.ID, storeID, "OWNER") {
		return
	}
	var referenced bool
	if err := s.db.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM "notification_deliveries" WHERE "ruleId"=$1 AND "storeId"=$2)`, ruleID, storeID).Scan(&referenced); err != nil {
		s.internalError(w, err)
		return
	}
	if referenced {
		writeError(w, http.StatusConflict, "Conflict", "This rule has delivery history. Disable it with PATCH isEnabled=false instead of deleting it.")
		return
	}
	result, err := s.db.Exec(r.Context(), `DELETE FROM "notification_rules" WHERE "id"=$1 AND "storeId"=$2`, ruleID, storeID)
	if err != nil {
		if foreignKeyViolation(err) {
			writeError(w, http.StatusConflict, "Conflict", "This rule has delivery history. Disable it with PATCH isEnabled=false instead of deleting it.")
			return
		}
		s.internalError(w, err)
		return
	}
	if result.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "Not Found", "Notification rule was not found.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func scanNotificationRuleChannel(row rowScanner, item *notificationRuleChannelResponse) error {
	return row.Scan(&item.ID, &item.RuleID, &item.EndpointID, &item.EndpointProvider, &item.EndpointLabel, &item.Priority, &item.FallbackDelaySeconds, &item.IsEnabled, &item.CreatedAt, &item.UpdatedAt)
}

const channelSelectColumns = `rc."id",rc."ruleId",rc."endpointId",e."provider"::text,e."label",rc."priority",rc."fallbackDelaySeconds",rc."isEnabled",rc."createdAt",rc."updatedAt"`

func requireStoreRule(w http.ResponseWriter, ctx context.Context, s *Server, storeID, ruleID string) bool {
	var exists bool
	err := s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM "notification_rules" WHERE "id"=$1 AND "storeId"=$2)`, ruleID, storeID).Scan(&exists)
	if err != nil {
		s.internalError(w, err)
		return false
	}
	if !exists {
		writeError(w, http.StatusNotFound, "Not Found", "Notification rule was not found.")
		return false
	}
	return true
}

func requireStoreEndpoint(w http.ResponseWriter, ctx context.Context, s *Server, storeID, endpointID string) bool {
	var exists bool
	err := s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM "notification_endpoints" WHERE "id"=$1 AND "storeId"=$2)`, endpointID, storeID).Scan(&exists)
	if err != nil {
		s.internalError(w, err)
		return false
	}
	if !exists {
		writeError(w, http.StatusNotFound, "Not Found", "Notification endpoint was not found.")
		return false
	}
	return true
}

func (s *Server) listNotificationRuleChannels(w http.ResponseWriter, r *http.Request, user principal) {
	storeID, ruleID := r.PathValue("storeId"), r.PathValue("ruleId")
	if !requireUUIDPath(w, storeID, ruleID) || !s.requireRole(w, r.Context(), user.ID, storeID, "OWNER") || !requireStoreRule(w, r.Context(), s, storeID, ruleID) {
		return
	}
	rows, err := s.db.Query(r.Context(), `SELECT `+channelSelectColumns+` FROM "notification_rule_channels" rc JOIN "notification_endpoints" e ON e."id"=rc."endpointId" WHERE rc."ruleId"=$1 AND rc."storeId"=$2 ORDER BY rc."priority",e."id"`, ruleID, storeID)
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer rows.Close()
	items := []notificationRuleChannelResponse{}
	for rows.Next() {
		var item notificationRuleChannelResponse
		if err := scanNotificationRuleChannel(rows, &item); err != nil {
			s.internalError(w, err)
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func ruleChannelConflict(err error) (string, bool) {
	pgError, ok := err.(*pgconn.PgError)
	if !ok || pgError.Code != "23505" {
		return "", false
	}
	switch pgError.ConstraintName {
	case "notification_rule_channels_rule_endpoint_key":
		return "This endpoint is already routed in this rule.", true
	case "notification_rule_channels_rule_priority_key":
		return "This priority is already used by another channel in this rule.", true
	}
	return "This channel conflicts with an existing route in this rule.", true
}

func (s *Server) createNotificationRuleChannel(w http.ResponseWriter, r *http.Request, user principal) {
	storeID, ruleID := r.PathValue("storeId"), r.PathValue("ruleId")
	if !requireUUIDPath(w, storeID, ruleID) || !s.requireRole(w, r.Context(), user.ID, storeID, "OWNER") || !requireStoreRule(w, r.Context(), s, storeID, ruleID) {
		return
	}
	var input notificationRuleChannelInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.EndpointID == nil {
		writeError(w, http.StatusBadRequest, "Bad Request", "endpointId is required.")
		return
	}
	endpointID := strings.TrimSpace(*input.EndpointID)
	if _, err := uuid.Parse(endpointID); err != nil {
		writeError(w, http.StatusBadRequest, "Bad Request", "endpointId must be a UUID.")
		return
	}
	priority := 1
	if input.Priority != nil {
		priority = *input.Priority
	}
	fallbackDelaySeconds := 0
	if input.FallbackDelaySeconds != nil {
		fallbackDelaySeconds = *input.FallbackDelaySeconds
	}
	isEnabled := true
	if input.IsEnabled != nil {
		isEnabled = *input.IsEnabled
	}
	if priority < 1 || priority > maxPriorityValue {
		writeError(w, http.StatusBadRequest, "Bad Request", "priority must be between 1 and 999; lower values route first.")
		return
	}
	if fallbackDelaySeconds < 0 || fallbackDelaySeconds > 86400 {
		writeError(w, http.StatusBadRequest, "Bad Request", "fallbackDelaySeconds must be between 0 and 86400.")
		return
	}
	if !requireStoreEndpoint(w, r.Context(), s, storeID, endpointID) {
		return
	}
	var item notificationRuleChannelResponse
	err := s.db.QueryRow(r.Context(), `WITH inserted AS (
		INSERT INTO "notification_rule_channels" ("id","storeId","ruleId","endpointId","priority","fallbackDelaySeconds","isEnabled","updatedAt") VALUES ($1,$2,$3,$4,$5,$6,$7,NOW())
		RETURNING "id","ruleId","endpointId","priority","fallbackDelaySeconds","isEnabled","createdAt","updatedAt"
	)
	SELECT i."id",i."ruleId",i."endpointId",e."provider"::text,e."label",i."priority",i."fallbackDelaySeconds",i."isEnabled",i."createdAt",i."updatedAt"
	FROM inserted i JOIN "notification_endpoints" e ON e."id"=i."endpointId" WHERE e."storeId"=$2`,
		uuid.NewString(), storeID, ruleID, endpointID, priority, fallbackDelaySeconds, isEnabled).Scan(&item.ID, &item.RuleID, &item.EndpointID, &item.EndpointProvider, &item.EndpointLabel, &item.Priority, &item.FallbackDelaySeconds, &item.IsEnabled, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		if message, conflict := ruleChannelConflict(err); conflict {
			writeError(w, http.StatusConflict, "Conflict", message)
			return
		}
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) updateNotificationRuleChannel(w http.ResponseWriter, r *http.Request, user principal) {
	storeID, ruleID, channelID := r.PathValue("storeId"), r.PathValue("ruleId"), r.PathValue("channelId")
	if !requireUUIDPath(w, storeID, ruleID, channelID) || !s.requireRole(w, r.Context(), user.ID, storeID, "OWNER") {
		return
	}
	var input notificationRuleChannelUpdateInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if !input.provided() {
		writeError(w, http.StatusBadRequest, "Bad Request", "Request body must contain at least one updatable field.")
		return
	}
	if input.Priority != nil && (*input.Priority < 1 || *input.Priority > maxPriorityValue) {
		writeError(w, http.StatusBadRequest, "Bad Request", "priority must be between 1 and 999; lower values route first.")
		return
	}
	if input.FallbackDelaySeconds != nil && (*input.FallbackDelaySeconds < 0 || *input.FallbackDelaySeconds > 86400) {
		writeError(w, http.StatusBadRequest, "Bad Request", "fallbackDelaySeconds must be between 0 and 86400.")
		return
	}
	var item notificationRuleChannelResponse
	err := s.db.QueryRow(r.Context(), `WITH updated AS (
		UPDATE "notification_rule_channels" SET
			"priority"=COALESCE($4,"priority"),
			"fallbackDelaySeconds"=COALESCE($5,"fallbackDelaySeconds"),
			"isEnabled"=COALESCE($6,"isEnabled"),
			"updatedAt"=NOW()
		WHERE "id"=$1 AND "ruleId"=$2 AND "storeId"=$3
		RETURNING "id","ruleId","endpointId","priority","fallbackDelaySeconds","isEnabled","createdAt","updatedAt"
	)
	SELECT i."id",i."ruleId",i."endpointId",e."provider"::text,e."label",i."priority",i."fallbackDelaySeconds",i."isEnabled",i."createdAt",i."updatedAt"
	FROM updated i JOIN "notification_endpoints" e ON e."id"=i."endpointId" WHERE e."storeId"=$3`,
		channelID, ruleID, storeID, input.Priority, input.FallbackDelaySeconds, input.IsEnabled).Scan(&item.ID, &item.RuleID, &item.EndpointID, &item.EndpointProvider, &item.EndpointLabel, &item.Priority, &item.FallbackDelaySeconds, &item.IsEnabled, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			writeError(w, http.StatusNotFound, "Not Found", "Notification rule channel was not found.")
			return
		}
		if message, conflict := ruleChannelConflict(err); conflict {
			writeError(w, http.StatusConflict, "Conflict", message)
			return
		}
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) removeNotificationRuleChannel(w http.ResponseWriter, r *http.Request, user principal) {
	storeID, ruleID, channelID := r.PathValue("storeId"), r.PathValue("ruleId"), r.PathValue("channelId")
	if !requireUUIDPath(w, storeID, ruleID, channelID) || !s.requireRole(w, r.Context(), user.ID, storeID, "OWNER") {
		return
	}
	var referenced bool
	if err := s.db.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM "notification_deliveries" WHERE "ruleChannelId"=$1 AND "storeId"=$2)`, channelID, storeID).Scan(&referenced); err != nil {
		s.internalError(w, err)
		return
	}
	if referenced {
		writeError(w, http.StatusConflict, "Conflict", "This channel has delivery history. Disable it with PATCH isEnabled=false instead of deleting it.")
		return
	}
	result, err := s.db.Exec(r.Context(), `DELETE FROM "notification_rule_channels" WHERE "id"=$1 AND "ruleId"=$2 AND "storeId"=$3`, channelID, ruleID, storeID)
	if err != nil {
		if foreignKeyViolation(err) {
			writeError(w, http.StatusConflict, "Conflict", "This channel has delivery history. Disable it with PATCH isEnabled=false instead of deleting it.")
			return
		}
		s.internalError(w, err)
		return
	}
	if result.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "Not Found", "Notification rule channel was not found.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func foreignKeyViolation(err error) bool {
	pgError, ok := err.(*pgconn.PgError)
	return ok && pgError.Code == "23503"
}
