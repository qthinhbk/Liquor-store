package notifications

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"
)

// CheckCredentialBindings is read-only. Refuse to start an enabled worker with
// incomplete deployment bindings instead of consuming queued alert attempts.
func CheckCredentialBindings(ctx context.Context, db *pgxpool.Pool, bindings []CredentialBinding) error {
	rows, err := db.Query(ctx, `SELECT "storeId","provider"::text,COALESCE("providerAccountRef",''),"credentialRef" FROM "notification_endpoints" WHERE "isEnabled"=true`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var request SendRequest
		if err := rows.Scan(&request.StoreID, &request.Provider, &request.ProviderAccountRef, &request.CredentialRef); err != nil {
			return err
		}
		if AuthorizeCredential(bindings, request) != nil {
			return errors.New("enabled notification endpoint has no matching credential binding; configure NOTIFICATION_CREDENTIAL_BINDINGS before starting the worker")
		}
	}
	return rows.Err()
}
