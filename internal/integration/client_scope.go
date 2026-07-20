package integration

import (
	"database/sql"
	"net/http"
	"strings"
	"time"

	"cms/internal/asset"
	"cms/internal/client"
	"cms/internal/platform/apperror"
	"cms/internal/platform/httpx"
)

func ClientAssetScope(service *client.Service) asset.PublishedScopeProvider {
	return func(r *http.Request) (asset.PublishedDownloadScope, error) {
		values := r.Header.Values("Authorization")
		if len(values) == 0 {
			return asset.PublishedDownloadScope{}, &apperror.Error{Kind: apperror.KindUnauthenticated, Code: "api_key_required", Message: "缺少 API Key"}
		}
		if len(values) != 1 || strings.Contains(values[0], ",") {
			return asset.PublishedDownloadScope{}, invalidAPIKey()
		}
		parts := strings.Split(values[0], " ")
		if len(parts) != 2 || parts[0] != "Bearer" || parts[1] == "" {
			return asset.PublishedDownloadScope{}, invalidAPIKey()
		}
		key, err := service.Authenticate(r.Context(), parts[1])
		if err != nil {
			return asset.PublishedDownloadScope{}, err
		}
		return asset.PublishedDownloadScope{AllowedModelIDs: key.ModelIDs}, nil
	}
}

type ClientAssetHandler struct {
	DB     *sql.DB
	Client *client.Service
	Assets *asset.Service
}

func (h ClientAssetHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/content/v1/assets/{asset_id}", h.download)
}

func (h ClientAssetHandler) download(w http.ResponseWriter, r *http.Request) {
	raw, err := bearerToken(r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	key, err := h.Client.Authenticate(r.Context(), raw)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	tx, err := h.DB.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	defer tx.Rollback()
	var revoked, expires sql.NullTime
	if err = tx.QueryRowContext(r.Context(), `SELECT revoked_at,expires_at FROM api_keys WHERE id=? FOR UPDATE`, key.ID).Scan(&revoked, &expires); err != nil {
		httpx.WriteError(w, r, invalidAPIKey())
		return
	}
	if revoked.Valid {
		httpx.WriteError(w, r, &apperror.Error{Kind: apperror.KindUnauthenticated, Code: "api_key_revoked", Message: "API Key 已撤销"})
		return
	}
	if expires.Valid && !time.Now().UTC().Before(expires.Time) {
		httpx.WriteError(w, r, &apperror.Error{Kind: apperror.KindUnauthenticated, Code: "api_key_expired", Message: "API Key 已过期"})
		return
	}
	rows, err := tx.QueryContext(r.Context(), `SELECT model_id FROM api_key_model_scopes WHERE api_key_id=? ORDER BY model_id`, key.ID)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	modelIDs := []string{}
	for rows.Next() {
		var modelID string
		if err = rows.Scan(&modelID); err != nil {
			break
		}
		modelIDs = append(modelIDs, modelID)
	}
	if closeErr := rows.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = rows.Err()
	}
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	signed, err := h.Assets.PublishedDownload(r.Context(), asset.PublishedDownloadScope{AllowedModelIDs: modelIDs}, r.PathValue("asset_id"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if err = tx.Commit(); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	w.Header().Set("Location", signed.URL)
	w.Header().Set("Cache-Control", "private, no-store")
	w.WriteHeader(http.StatusFound)
}

func bearerToken(r *http.Request) (string, error) {
	values := r.Header.Values("Authorization")
	if len(values) == 0 {
		return "", &apperror.Error{Kind: apperror.KindUnauthenticated, Code: "api_key_required", Message: "缺少 API Key"}
	}
	if len(values) != 1 || strings.Contains(values[0], ",") {
		return "", invalidAPIKey()
	}
	parts := strings.Split(values[0], " ")
	if len(parts) != 2 || parts[0] != "Bearer" || parts[1] == "" {
		return "", invalidAPIKey()
	}
	return parts[1], nil
}

func invalidAPIKey() error {
	return &apperror.Error{Kind: apperror.KindUnauthenticated, Code: "invalid_api_key", Message: "API Key 无效"}
}
