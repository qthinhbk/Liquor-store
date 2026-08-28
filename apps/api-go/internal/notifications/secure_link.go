package notifications

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrSecureLinkUnavailable = errors.New("secure review links are not configured")
	ErrSecureLinkNotFound    = errors.New("secure review link was not found")
)

type ReviewLinkInput struct {
	StoreID    string
	AlertID    string
	EvidenceID string
	DeliveryID string
}

type ReviewLinkBuilder interface {
	CreateReviewLink(ctx context.Context, input ReviewLinkInput) (string, error)
}

type SecureReviewService struct {
	db             *pgxpool.Pool
	publicBaseURL  string
	evidenceOrigin *url.URL
	originToken    string
	ttl            time.Duration
	httpClient     *http.Client
	now            func() time.Time
}

func NewSecureReviewService(db *pgxpool.Pool, publicBaseURL, evidenceOriginBaseURL, originToken string, ttl time.Duration) (*SecureReviewService, error) {
	if db == nil || strings.TrimSpace(publicBaseURL) == "" || strings.TrimSpace(evidenceOriginBaseURL) == "" || ttl <= 0 {
		return nil, ErrSecureLinkUnavailable
	}
	publicURL, err := parseFixedOrigin(publicBaseURL)
	if err != nil {
		return nil, err
	}
	evidenceURL, err := parseFixedOrigin(evidenceOriginBaseURL)
	if err != nil {
		return nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 10 * time.Second
	client := &http.Client{
		Transport: transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return &SecureReviewService{
		db: db, publicBaseURL: strings.TrimRight(publicURL.String(), "/"),
		evidenceOrigin: evidenceURL, originToken: strings.TrimSpace(originToken),
		ttl: ttl, httpClient: client, now: time.Now,
	}, nil
}

func parseFixedOrigin(raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("secure review origin must be an absolute HTTP(S) URL without credentials, query, or fragment")
	}
	return parsed, nil
}

func (s *SecureReviewService) CreateReviewLink(ctx context.Context, input ReviewLinkInput) (string, error) {
	if s == nil || s.db == nil {
		return "", ErrSecureLinkUnavailable
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	digest := sha256.Sum256([]byte(token))
	expiresAt := s.now().UTC().Add(s.ttl)
	var deliveryID any
	if strings.TrimSpace(input.DeliveryID) != "" {
		deliveryID = input.DeliveryID
	}
	var id string
	err := s.db.QueryRow(ctx, `INSERT INTO "notification_video_links" ("id","tokenHash","storeId","alertId","evidenceId","deliveryId","expiresAt") SELECT $1,$2,a."storeId",e."alertId",e."id",$6,$7 FROM "alert_evidence" e JOIN "alerts" a ON a."id"=e."alertId" WHERE e."id"=$3 AND e."alertId"=$4 AND a."storeId"=$5 RETURNING "id"`,
		uuid.NewString(), digest[:], input.EvidenceID, input.AlertID, input.StoreID, deliveryID, expiresAt).Scan(&id)
	if err != nil {
		if err == pgx.ErrNoRows {
			return "", ErrSecureLinkNotFound
		}
		return "", err
	}
	return s.publicBaseURL + "/api/v1/notification-review/" + token, nil
}

func (s *SecureReviewService) ServeToken(w http.ResponseWriter, r *http.Request, token string) {
	if s == nil || s.db == nil {
		http.Error(w, "Review video is temporarily unavailable.", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "Method not allowed.", http.StatusMethodNotAllowed)
		return
	}
	if len(token) < 40 || len(token) > 64 {
		http.NotFound(w, r)
		return
	}
	digest := sha256.Sum256([]byte(token))
	var storageKey, mimeType string
	err := s.db.QueryRow(r.Context(), `UPDATE "notification_video_links" l SET "accessCount"=l."accessCount"+1,"lastAccessedAt"=NOW() FROM "alert_evidence" e WHERE l."tokenHash"=$1 AND l."revokedAt" IS NULL AND l."expiresAt">NOW() AND e."id"=l."evidenceId" AND e."alertId"=l."alertId" RETURNING e."storageKey",e."mimeType"`, digest[:]).Scan(&storageKey, &mimeType)
	if err != nil {
		if err == pgx.ErrNoRows {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "Review video is temporarily unavailable.", http.StatusServiceUnavailable)
		return
	}
	originURL, err := s.originURL(storageKey)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	request, err := http.NewRequestWithContext(r.Context(), r.Method, originURL, nil)
	if err != nil {
		http.Error(w, "Review video is temporarily unavailable.", http.StatusServiceUnavailable)
		return
	}
	for _, header := range []string{"Range", "If-Range", "If-None-Match", "If-Modified-Since"} {
		if value := r.Header.Get(header); value != "" {
			request.Header.Set(header, value)
		}
	}
	if s.originToken != "" {
		request.Header.Set("Authorization", "Bearer "+s.originToken)
	}
	response, err := s.httpClient.Do(request)
	if err != nil {
		http.Error(w, "Review video is temporarily unavailable.", http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusPartialContent && response.StatusCode != http.StatusNotModified {
		if response.StatusCode == http.StatusNotFound {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "Review video is temporarily unavailable.", http.StatusBadGateway)
		return
	}
	for _, header := range []string{"Accept-Ranges", "Content-Length", "Content-Range", "ETag", "Last-Modified"} {
		if value := response.Header.Get(header); value != "" {
			w.Header().Set(header, value)
		}
	}
	if mimeType == "" {
		mimeType = response.Header.Get("Content-Type")
	}
	if mimeType != "" {
		w.Header().Set("Content-Type", mimeType)
	}
	w.Header().Set("Cache-Control", "private, no-store, max-age=0")
	w.Header().Set("Content-Disposition", `inline; filename="alert-evidence"`)
	w.WriteHeader(response.StatusCode)
	if r.Method == http.MethodGet && response.StatusCode != http.StatusNotModified {
		_, _ = io.Copy(w, response.Body)
	}
}

func (s *SecureReviewService) originURL(storageKey string) (string, error) {
	key := strings.TrimSpace(storageKey)
	if key == "" || strings.HasPrefix(key, "/") || strings.Contains(key, "\\") || strings.Contains(key, "://") || strings.ContainsRune(key, 0) {
		return "", errors.New("invalid evidence storage key")
	}
	parts := strings.Split(key, "/")
	encoded := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", errors.New("invalid evidence storage key")
		}
		encoded = append(encoded, url.PathEscape(part))
	}
	origin := *s.evidenceOrigin
	origin.Path = strings.TrimRight(origin.Path, "/") + "/" + key
	origin.RawPath = strings.TrimRight(origin.EscapedPath(), "/") + "/" + strings.Join(encoded, "/")
	return origin.String(), nil
}
