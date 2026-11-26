package hub

import (
	"context"
	"net/http"

	"github.com/CodexCircle/Regulus/internal/store"
)

// StoreSource is the plugin store's read side (internal/store). *store.Service
// implements it.
type StoreSource interface {
	Catalogue(ctx context.Context) store.CatalogueJSON
}

// storeHandler serves GET /api/store: the sources with their state and the
// plugins they offer, each version marked compatible or not with the reason.
func storeHandler(src StoreSource) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if src == nil {
			writeJSONBody(w, http.StatusOK, store.CatalogueJSON{Sources: []store.SourceJSON{}, Plugins: []store.PluginJSON{}})
			return
		}
		writeJSONBody(w, http.StatusOK, src.Catalogue(r.Context()))
	}
}

// storeInstallHandler serves POST /api/store/plugins/{id}/install. Installing
// needs the root helper, which is not built, so it says so and does nothing:
// there is no fake success.
func storeInstallHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSONError(w, http.StatusNotImplemented, "installation needs the root helper, not built yet")
	}
}
