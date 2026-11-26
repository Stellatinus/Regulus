package hub

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/CodexCircle/Regulus/internal/auth"
	"github.com/CodexCircle/Regulus/internal/store"
)

// StoreSource is the plugin store's read side (internal/store). *store.Service
// implements it.
type StoreSource interface {
	Catalogue(ctx context.Context) store.CatalogueJSON
}

// StoreInstaller is the store's write side: install, update and remove a
// plugin. *store.Installer implements it.
type StoreInstaller interface {
	Install(ctx context.Context, by, id, version, source string) (store.Outcome, error)
	Update(ctx context.Context, by, id, version string) (store.Outcome, error)
	Remove(ctx context.Context, by, id string) (store.Outcome, error)
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

type storeBody struct {
	Version string `json:"version"`
	Source  string `json:"source"`
}

// readStoreBody reads the optional JSON body of a store request. An empty body
// is the same as {}.
func readStoreBody(w http.ResponseWriter, r *http.Request) (storeBody, bool) {
	var b storeBody
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&b); err != nil && !errors.Is(err, io.EOF) {
		writeJSONErrorCode(w, http.StatusBadRequest, "the request body is not valid JSON", codeInvalidJSON)
		return b, false
	}
	return b, true
}

// storeStatus is the HTTP status of a refusal of the store, by its code.
func storeStatus(err error) int {
	var e *store.Error
	if !errors.As(err, &e) {
		return http.StatusInternalServerError
	}
	switch e.Code() {
	case store.ErrCodeNotFound, store.ErrCodeNotInstalled:
		return http.StatusNotFound
	case store.ErrCodeSourceUnknown:
		return http.StatusBadRequest
	case store.ErrCodeAlreadyInstalled, store.ErrCodeBusy, store.ErrCodeUpToDate, store.ErrCodeSourceAmbiguous,
		store.ErrCodeNoSource, store.ErrCodePluginsOff:
		return http.StatusConflict
	case store.ErrCodeNoCompatible, store.ErrCodeVersionNotOffer:
		return http.StatusUnprocessableEntity
	case store.ErrCodeSourceUnavail, store.ErrCodeDownload:
		return http.StatusBadGateway
	case store.ErrCodeHelperTimeout:
		return http.StatusGatewayTimeout
	case store.ErrCodeHelperFailed:
		switch e.Helper {
		case store.CodeNotVerified, store.CodeIncompatible, store.CodeYanked, store.CodeChecksum, store.CodeUnsafePackage,
			store.CodeBadManifest, store.CodeBadRequest, store.CodeUnknownSource:
			return http.StatusUnprocessableEntity
		case store.CodeAlreadyInstalled, store.CodeNotInstalled, store.CodeSourceMismatch, store.CodeHandInstalled:
			return http.StatusConflict
		}
	}
	return http.StatusInternalServerError
}

// writeStoreError answers a refusal with its text and its stable code (and, if
// the root helper refused, its code as args.helper_code).
func writeStoreError(w http.ResponseWriter, err error) {
	var e *store.Error
	if errors.As(err, &e) {
		body := map[string]any{"error": e.Error(), "code": e.Code()}
		if args := e.Args(); len(args) > 0 {
			body["args"] = args
		}
		writeJSONBody(w, storeStatus(err), body)
		return
	}
	writeInternal(w)
}

func storeActor(r *http.Request) string {
	if u, ok := auth.UserFrom(r.Context()); ok {
		return u.Username
	}
	return ""
}

func noInstaller(w http.ResponseWriter) {
	writeJSONBody(w, http.StatusConflict, map[string]any{"error": "plugins are off on this hub", "code": store.ErrCodePluginsOff})
}

// storeInstallHandler serves POST /api/store/plugins/{id}/install with the
// optional body {"version": "...", "source": "..."}. It waits for the root
// helper and answers 200 with the outcome, or a refusal with a code.
func storeInstallHandler(in StoreInstaller) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if in == nil {
			noInstaller(w)
			return
		}
		b, ok := readStoreBody(w, r)
		if !ok {
			return
		}
		out, err := in.Install(r.Context(), storeActor(r), r.PathValue("id"), b.Version, b.Source)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		writeJSONBody(w, http.StatusOK, out)
	}
}

// storeUpdateHandler serves POST /api/store/plugins/{id}/update with the
// optional body {"version": "..."}: the update comes from the source the
// plugin was installed from.
func storeUpdateHandler(in StoreInstaller) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if in == nil {
			noInstaller(w)
			return
		}
		b, ok := readStoreBody(w, r)
		if !ok {
			return
		}
		out, err := in.Update(r.Context(), storeActor(r), r.PathValue("id"), b.Version)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		writeJSONBody(w, http.StatusOK, out)
	}
}

// storeRemoveHandler serves DELETE /api/store/plugins/{id}.
func storeRemoveHandler(in StoreInstaller) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if in == nil {
			noInstaller(w)
			return
		}
		out, err := in.Remove(r.Context(), storeActor(r), r.PathValue("id"))
		if err != nil {
			writeStoreError(w, err)
			return
		}
		writeJSONBody(w, http.StatusOK, out)
	}
}
