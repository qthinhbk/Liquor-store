package server

import (
	"encoding/json"
	"net/mail"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgconn"
)

var storeCodePattern = regexp.MustCompile(`^[a-z0-9-]{3,40}$`)

func validEmail(value string) bool {
	parsed, err := mail.ParseAddress(value)
	return err == nil && strings.EqualFold(parsed.Address, value) && len(value) <= 320
}

func validLength(value string, minimum, maximum int) bool {
	length := utf8.RuneCountInString(strings.TrimSpace(value))
	return length >= minimum && length <= maximum
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func uniqueViolation(err error) bool {
	pgError, ok := err.(*pgconn.PgError)
	return ok && pgError.Code == "23505"
}

func validNormalizedPolygon(raw json.RawMessage) bool {
	var points [][]float64
	if err := json.Unmarshal(raw, &points); err != nil || len(points) < 3 {
		return false
	}
	for _, point := range points {
		if len(point) != 2 || point[0] < 0 || point[0] > 1 || point[1] < 0 || point[1] > 1 {
			return false
		}
	}
	return true
}
