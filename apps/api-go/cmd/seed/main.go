package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/liquor-store/security-api/internal/config"
)

type cameraSeed struct{ Name, Location, Ref string }

type zoneSeed struct {
	CameraRef, Name, Kind, Category string
	Polygon                         [][]float64
	DwellSeconds                    *int
}

type alertSeed struct {
	EventID, CameraRef, ZoneName, Type, Severity, Status, Category string
	Confidence                                                     float64
	MinutesAgo                                                     int
	ResolutionNote                                                 string
	Metadata                                                       map[string]any
}

var cameras = []cameraSeed{
	{"Cold drink dispenser (Stream 1)", "Cold drink dispenser", "record-cold-drink-dispenser-152956"},
	{"Cold drink dispenser (Stream 2)", "Cold drink dispenser", "record-cold-drink-dispenser-153308"},
	{"Cold drink fridge", "Cold drink fridge", "record-cold-drink-fridge-153353"},
	{"Cold storage", "Cold storage", "record-cold-storage-153534"},
	{"Control room", "Control room", "record-control-room-154343"},
	{"Counter top view (Stream 1)", "Payment counter", "record-counter-top-view-153244"},
	{"Counter top view (Stream 2)", "Payment counter", "record-counter-top-view-154014"},
	{"Counter", "Payment counter", "record-counter-153921"},
	{"Entrance top view", "Entrance", "record-entrance-top-view-153712"},
	{"Foodbox", "Foodbox area", "record-foodbox-153112"},
	{"Kitchen foodbox", "Kitchen", "record-kitchen-foodbox-153050"},
	{"Kitchen", "Kitchen", "record-kitchen-153607"},
	{"Storage", "Storage", "record-storage-154138"},
	{"Store top corner view", "Store top corner", "record-store-top-corner-view-153819"},
	{"Whole store corner view", "Whole store corner", "record-whole-store-corner-view-153134"},
	{"Whole store top view (Stream 1)", "Whole store top", "record-whole-store-top-view-153423"},
	{"Whole store top view (Stream 2)", "Whole store top", "record-whole-store-top-view-155405"},
	{"Whole store", "Whole store", "record-whole-store-153158"},
}

var legacyCameraRefs = []string{
	"cold-drink-dispenser-01", "kitchen-foodbox", "foodbox", "whole-store-corner-view", "whole-store", "counter-top-view",
	"cold-drink-dispenser-07", "cold-drink-fridge", "whole-store-top-view", "cold-storage", "kitchen", "entrance-top-view",
	"store-top-corner-view", "counter", "counter-top-view-02", "storage", "bbq-area", "control-room", "backyard", "ac-fan",
	"gas-station-parking", "gas-station-01", "gas-station-02", "whole-store-top-view-02", "gas-station-03", "gas-station-04",
	"channel-27", "channel-28", "channel-29", "channel-30", "channel-31", "channel-32",
}

func intPtr(value int) *int { return &value }

var zones = []zoneSeed{
	{"record-counter-top-view-153244", "Employee side", "CASHIER", "EMPLOYEE", [][]float64{{0.05, 0.08}, {0.52, 0.08}, {0.52, 0.94}, {0.05, 0.94}}, nil},
	{"record-counter-top-view-153244", "Customer side", "CASHIER", "CUSTOMER", [][]float64{{0.54, 0.08}, {0.96, 0.08}, {0.96, 0.94}, {0.54, 0.94}}, nil},
	{"record-storage-154138", "Stockroom access", "STOCKROOM", "EMPLOYEE", [][]float64{{0.12, 0.15}, {0.88, 0.15}, {0.92, 0.9}, {0.08, 0.9}}, nil},
	{"record-entrance-top-view-153712", "Main entrance", "ENTRANCE", "CUSTOMER", [][]float64{{0.18, 0.18}, {0.82, 0.18}, {0.96, 0.96}, {0.04, 0.96}}, nil},
	{"record-whole-store-top-view-153423", "Premium spirits", "HIGH_VALUE", "CUSTOMER", [][]float64{{0.58, 0.18}, {0.9, 0.18}, {0.9, 0.76}, {0.58, 0.76}}, intPtr(90)},
	{"record-cold-storage-153534", "Cold storage door", "STOCKROOM", "EMPLOYEE", [][]float64{{0.22, 0.1}, {0.8, 0.1}, {0.88, 0.92}, {0.14, 0.92}}, nil},
}

// cctvVideoFixtures adapts the notification-shaped content expected by the
// reference PDSI UI to this project's domain and attaches short, browser-safe
// clips derived from the supplied CCTV recordings. These presentation fixtures
// are marked in metadata so they cannot be mistaken for AI production events.
func cctvVideoFixtures() []alertSeed {
	type mediaFixture struct {
		CameraRef, AlertType, Category, VideoURL, ThumbnailURL, TrackingURL, Summary string
		BoxX, BoxY, BoxWidth, BoxHeight                                              float64
	}
	media := []mediaFixture{
		{"record-counter-153921", "WEAPON_DETECTED", "UNKNOWN", "/demo/cctv-ch14-034329.mp4", "/demo/cctv-ch14-034329.jpg", "/demo/tracks/cctv-ch14-034329.json", "Emergency review: a possible dangerous object was detected in the store", .656318, .233285, .087923, .280522},
		{"record-counter-153921", "CASH_DRAWER_WITHOUT_CUSTOMER", "EMPLOYEE", "/demo/cctv-ch14-031430.mp4", "/demo/cctv-ch14-031430.jpg", "/demo/tracks/cctv-ch14-031430.json", "Counter camera context clip for cash drawer review", .269526, .186044, .070087, .307317},
		{"record-counter-153921", "SUSPICIOUS_CASH_HANDLING", "EMPLOYEE", "/demo/cctv-ch14-034100.mp4", "/demo/cctv-ch14-034100.jpg", "/demo/tracks/cctv-ch14-034100.json", "Counter camera context clip for cash handling review", .582944, .575873, .160734, .387876},
		{"record-counter-153921", "POS_VOID_OR_REFUND", "EMPLOYEE", "/demo/cctv-ch14-034329.mp4", "/demo/cctv-ch14-034329.jpg", "/demo/tracks/cctv-ch14-034329.json", "Counter camera context clip for POS activity review", .656318, .233285, .087923, .280522},
		{"record-storage-154138", "UNAUTHORIZED_STOCKROOM_ACCESS", "EMPLOYEE", "/demo/cctv-ch16-042352.mp4", "/demo/cctv-ch16-042352.jpg", "/demo/tracks/cctv-ch16-042352.json", "Storage camera context clip for access review", .314192, .511679, .148318, .487024},
	}
	severities := []string{"LOW", "MEDIUM", "HIGH", "MEDIUM", "CRITICAL"}
	statuses := []string{"RESOLVED", "DISMISSED", "RESOLVED", "ACKNOWLEDGED"}
	fixtures := make([]alertSeed, 0, 14)
	for index := 0; index < 14; index++ {
		context := media[index%len(media)]
		status := statuses[index%len(statuses)]
		note := "Demo notification reviewed for the deployment preview."
		// Keep several PDSI-derived fixtures in the review queue so the video
		// interaction is immediately visible on the Alerts page.
		if index < 5 {
			status = "NEW"
			note = ""
		} else if status == "ACKNOWLEDGED" {
			note = "Demo notification is awaiting a final owner decision."
		}
		minutesAgo := 480 + index*720
		if index < 5 {
			minutesAgo = []int{2, 8, 16, 26, 40}[index]
		}
		fixtures = append(fixtures, alertSeed{
			EventID:        fmt.Sprintf("pdsi-ui-fixture-%03d", index+1),
			CameraRef:      context.CameraRef,
			Type:           context.AlertType,
			Severity:       severities[index%len(severities)],
			Status:         status,
			Category:       context.Category,
			Confidence:     .66 + float64(index%7)*.04,
			MinutesAgo:     minutesAgo,
			ResolutionNote: note,
			Metadata: map[string]any{
				"summary":        context.Summary,
				"sourceSystem":   "cctv-upload-fixture",
				"sampleOnly":     true,
				"thumbnailUrl":   context.ThumbnailURL,
				"videoUrl":       context.VideoURL,
				"trackingUrl":    context.TrackingURL,
				"trackingModel":  "YOLO26n + ByteTrack",
				"detectionLabel": "Person",
				"detectionBox": map[string]any{
					"x": context.BoxX, "y": context.BoxY, "width": context.BoxWidth, "height": context.BoxHeight,
				},
				"notificationCode": fmt.Sprintf("UI-NOTIF-%04d", index+1),
				"violationCount":   index%3 + 1,
			},
		})
	}
	return fixtures
}

func main() {
	cfg, err := config.Load()
	if err != nil {
		fatal(err)
	}
	storeCode := os.Getenv("SEED_STORE_CODE")
	if storeCode == "" {
		storeCode = "liquor-store-demo"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		fatal(err)
	}
	defer conn.Close(context.Background())

	tx, err := conn.Begin(ctx)
	if err != nil {
		fatal(err)
	}
	defer tx.Rollback(context.Background())

	var storeID string
	if err := tx.QueryRow(ctx, `SELECT "id" FROM "stores" WHERE "code"=$1`, storeCode).Scan(&storeID); err != nil {
		fatal(fmt.Errorf("store %q not found: %w", storeCode, err))
	}
	var ownerID *string
	var owner string
	if err := tx.QueryRow(ctx, `SELECT "userId" FROM "store_memberships" WHERE "storeId"=$1 AND "role"='OWNER' ORDER BY "createdAt" LIMIT 1`, storeID).Scan(&owner); err == nil {
		ownerID = &owner
	} else if err != pgx.ErrNoRows {
		fatal(err)
	}

	if _, err := tx.Exec(ctx, `DELETE FROM "alerts" WHERE "storeId"=$1 AND "sourceEventId" LIKE 'demo-alert-%'`, storeID); err != nil {
		fatal(err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM "cameras" WHERE "storeId"=$1 AND "streamGatewayRef"=ANY($2::text[])`, storeID, legacyCameraRefs); err != nil {
		fatal(err)
	}

	cameraIDs := make(map[string]string, len(cameras))
	for _, camera := range cameras {
		var id string
		err = tx.QueryRow(ctx, `INSERT INTO "cameras" ("id","storeId","name","location","protocol","streamGatewayRef","status","isEnabled","updatedAt") VALUES ($1,$2,$3,$4,'RTSP',$5,'ONLINE',true,NOW()) ON CONFLICT ("streamGatewayRef") DO UPDATE SET "name"=EXCLUDED."name","location"=EXCLUDED."location","storeId"=EXCLUDED."storeId","status"='ONLINE',"isEnabled"=true,"updatedAt"=NOW() RETURNING "id"`, uuid.NewString(), storeID, camera.Name, camera.Location, camera.Ref).Scan(&id)
		if err != nil {
			fatal(err)
		}
		cameraIDs[camera.Ref] = id
	}

	zoneIDs := make(map[string]string, len(zones))
	for _, zone := range zones {
		cameraID := cameraIDs[zone.CameraRef]
		polygon, _ := json.Marshal(zone.Polygon)
		var id string
		err = tx.QueryRow(ctx, `SELECT "id" FROM "camera_zones" WHERE "cameraId"=$1 AND "name"=$2 LIMIT 1`, cameraID, zone.Name).Scan(&id)
		if err == pgx.ErrNoRows {
			id = uuid.NewString()
			_, err = tx.Exec(ctx, `INSERT INTO "camera_zones" ("id","cameraId","name","kind","expectedPersonCategory","polygon","dwellThresholdSeconds","isEnabled","updatedAt") VALUES ($1,$2,$3,$4,$5,$6::jsonb,$7,true,NOW())`, id, cameraID, zone.Name, zone.Kind, zone.Category, polygon, zone.DwellSeconds)
		} else if err == nil {
			_, err = tx.Exec(ctx, `UPDATE "camera_zones" SET "kind"=$1,"expectedPersonCategory"=$2,"polygon"=$3::jsonb,"dwellThresholdSeconds"=$4,"isEnabled"=true,"updatedAt"=NOW() WHERE "id"=$5`, zone.Kind, zone.Category, polygon, zone.DwellSeconds, id)
		}
		if err != nil {
			fatal(err)
		}
		zoneIDs[zone.CameraRef+"|"+zone.Name] = id
	}

	seedAlerts := cctvVideoFixtures()
	now := time.Now().UTC()
	for _, alert := range seedAlerts {
		detectedAt := now.Add(-time.Duration(alert.MinutesAgo) * time.Minute)
		observedStart := detectedAt.Add(-18 * time.Second)
		metadata, _ := json.Marshal(alert.Metadata)
		cameraID := cameraIDs[alert.CameraRef]
		var zoneID any
		if alert.ZoneName != "" {
			zoneID = zoneIDs[alert.CameraRef+"|"+alert.ZoneName]
		}
		var acknowledgedAt any
		var acknowledgedBy any
		var note any
		if alert.Status != "NEW" {
			acknowledgedAt = detectedAt.Add(4 * time.Minute)
			acknowledgedBy = ownerID
			if alert.ResolutionNote != "" {
				note = alert.ResolutionNote
			}
		}
		var alertID string
		err = tx.QueryRow(ctx, `INSERT INTO "alerts" ("id","sourceEventId","correlationId","storeId","cameraId","zoneId","type","severity","status","subjectPersonCategory","confidence","detectedAt","observedStartAt","observedEndAt","acknowledgedAt","acknowledgedById","resolutionNote","metadata","updatedAt") VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$12,$14,$15,$16,$17::jsonb,NOW()) ON CONFLICT ("sourceEventId") DO UPDATE SET "storeId"=EXCLUDED."storeId","cameraId"=EXCLUDED."cameraId","zoneId"=EXCLUDED."zoneId","type"=EXCLUDED."type","severity"=EXCLUDED."severity","status"=EXCLUDED."status","subjectPersonCategory"=EXCLUDED."subjectPersonCategory","confidence"=EXCLUDED."confidence","detectedAt"=EXCLUDED."detectedAt","observedStartAt"=EXCLUDED."observedStartAt","observedEndAt"=EXCLUDED."observedEndAt","acknowledgedAt"=EXCLUDED."acknowledgedAt","acknowledgedById"=EXCLUDED."acknowledgedById","resolutionNote"=EXCLUDED."resolutionNote","metadata"=EXCLUDED."metadata","updatedAt"=NOW() RETURNING "id"`, uuid.NewString(), alert.EventID, "demo-session-2026", storeID, cameraID, zoneID, alert.Type, alert.Severity, alert.Status, alert.Category, alert.Confidence, detectedAt, observedStart, acknowledgedAt, acknowledgedBy, note, metadata).Scan(&alertID)
		if err != nil {
			fatal(err)
		}
		storageKey := "demo/evidence/" + alert.EventID + ".mp4"
		_, err = tx.Exec(ctx, `INSERT INTO "alert_evidence" ("id","alertId","storageKey","mimeType","durationSeconds","startsAt","endsAt") VALUES ($1,$2,$3,'video/mp4',18,$4,$5) ON CONFLICT ("storageKey") DO UPDATE SET "alertId"=EXCLUDED."alertId","startsAt"=EXCLUDED."startsAt","endsAt"=EXCLUDED."endsAt"`, uuid.NewString(), alertID, storageKey, observedStart, detectedAt)
		if err != nil {
			fatal(err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		fatal(err)
	}
	fmt.Printf("Demo data ready for %s: %d cameras, %d zones, %d alerts.\n", storeCode, len(cameras), len(zones), len(seedAlerts))
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
