package server

import (
	"crypto/hmac"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/liquor-store/security-api/internal/notifications"
)

const whatsAppWebhookBodyLimit = 1 << 20

func (s *Server) verifyWhatsAppWebhook(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	configuredToken := strings.TrimSpace(s.config.WhatsAppWebhookVerifyToken)
	if configuredToken == "" || strings.TrimSpace(s.config.WhatsAppAppSecret) == "" {
		writeError(w, http.StatusServiceUnavailable, "Service Unavailable", "WhatsApp webhook is not configured.")
		return
	}
	query := r.URL.Query()
	providedToken := query.Get("hub.verify_token")
	challenge := query.Get("hub.challenge")
	if query.Get("hub.mode") != "subscribe" || challenge == "" || len(challenge) > 1024 || !hmac.Equal([]byte(providedToken), []byte(configuredToken)) {
		writeError(w, http.StatusForbidden, "Forbidden", "WhatsApp webhook verification failed.")
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(challenge))
}

func (s *Server) receiveWhatsAppWebhook(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	appSecret := strings.TrimSpace(s.config.WhatsAppAppSecret)
	if appSecret == "" || strings.TrimSpace(s.config.WhatsAppWebhookVerifyToken) == "" {
		writeError(w, http.StatusServiceUnavailable, "Service Unavailable", "WhatsApp webhook is not configured.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, whatsAppWebhookBodyLimit)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "Content Too Large", "WhatsApp webhook body exceeds the allowed size.")
			return
		}
		writeError(w, http.StatusBadRequest, "Bad Request", "Unable to read WhatsApp webhook body.")
		return
	}
	if !notifications.VerifyWhatsAppWebhookSignature(appSecret, body, r.Header.Get("X-Hub-Signature-256")) {
		writeError(w, http.StatusUnauthorized, "Unauthorized", "WhatsApp webhook signature is invalid.")
		return
	}
	events, err := notifications.ParseWhatsAppStatusEvents(body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Bad Request", "Invalid WhatsApp webhook payload.")
		return
	}
	if _, err := notifications.ApplyWhatsAppStatusEvents(r.Context(), s.db, events); err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"received": true})
}
