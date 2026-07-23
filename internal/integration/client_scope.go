package integration

import (
	"context"
	"database/sql"
	"net/http"

	"cms/internal/asset"
	"cms/internal/client"
	"cms/internal/platform/database"
	"cms/internal/platform/httpx"
)

type TransactionRunner interface {
	WithinTx(context.Context, *sql.TxOptions, func(database.Querier) error) error
}

type ClientAssetHandler struct {
	Tx         TransactionRunner
	Client     *client.Service
	Assets     *asset.Service
	Protection *client.Protection
}

func (h ClientAssetHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/content/v1/assets/{asset_id}", h.download)
}

func (h ClientAssetHandler) download(w http.ResponseWriter, r *http.Request) {
	raw, err := client.ParseBearerToken(r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	key, err := h.Client.Authenticate(r.Context(), raw)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if h.Protection != nil {
		release, retry, protectErr := h.Protection.Acquire(key.ID, httpx.ClientIPFromRequest(r))
		if protectErr != nil {
			client.WriteProtectionError(w, r, retry, protectErr)
			return
		}
		defer release()
	}
	var download asset.PublishedDownload
	err = h.Tx.WithinTx(r.Context(), nil, func(q database.Querier) error {
		key, err = h.Client.AuthenticateForDownload(r.Context(), q, raw)
		if err != nil {
			return err
		}
		download, err = h.Assets.ResolvePublishedDownload(r.Context(), q, asset.PublishedDownloadScope{AllowedModelIDs: key.ModelIDs, AllowedConfigNamespaceIDs: key.ConfigNamespaceIDs}, r.PathValue("asset_id"))
		return err
	})
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	h.Client.TouchLastUsed(r.Context(), key)
	signed, err := h.Assets.SignPublishedDownload(r.Context(), download)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	w.Header().Set("Location", signed.URL)
	w.Header().Set("Cache-Control", "private, no-store")
	w.WriteHeader(http.StatusFound)
}
