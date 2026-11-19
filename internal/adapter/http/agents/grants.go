package agents

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/vaultix/vaultix/internal/domain"
	agentuc "github.com/vaultix/vaultix/internal/usecase/agent"
)

// checkPartChecksum enforces the multipart integrity contract at the
// boundary: every part must carry the agent-observed sha256 (64 hex chars)
// of the exact bytes PUT plus that part's byte length. Pre-checksum agents
// are rejected with an actionable upgrade message — a multipart object
// whose parts were never hash-verified must never complete, let alone
// verify later.
func checkPartChecksum(number int, sha256Hex string, sizeBytes int64) error {
	if strings.TrimSpace(sha256Hex) == "" || sizeBytes <= 0 {
		return fmt.Errorf("%w: part %d: sha256 checksum and size_bytes are required; "+
			"upgrade the agent to a multipart-checksum release and re-upload",
			domain.ErrValidation, number)
	}
	if len(sha256Hex) != 64 || !isHexDigest(sha256Hex) {
		return fmt.Errorf("%w: part %d: sha256 must be 64 hex chars",
			domain.ErrValidation, number)
	}
	return nil
}

// isHexDigest reports whether s is all ASCII hex digits.
func isHexDigest(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F' {
			continue
		}
		return false
	}
	return true
}

func writeTooLarge(key string) error {
	return fmt.Errorf("%w: object %q exceeds the size ceiling", domain.ErrValidation, key)
}

// maxGrantObjectBytes caps one advisory object size (5 TiB, the S3 single
// object ceiling): larger values are malformed requests, not real objects.
const maxGrantObjectBytes = 5 << 40

// Grants stay bearer-authed like the rest of the agent protocol; every call
// is scoped to the credential's agent (never to a body-supplied identity).

// handleUploadGrants mints short-lived PUT grants for one job's objects.
// POST /api/agent/jobs/:id/upload-grants
func (h *handler) handleUploadGrants(w http.ResponseWriter, r *http.Request) {
	cred := credentialFrom(r.Context())
	if cred == nil {
		h.writeError(w, r, agentuc.ErrUnauthorized)
		return
	}
	if h.grants == nil {
		h.writeError(w, r, errNotConfigured)
		return
	}
	jobID, err := requireJobID(r)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	var req uploadGrantsRequest
	if err := decodeJSON(r, &req); err != nil {
		h.writeError(w, r, err)
		return
	}
	if err := h.check(req); err != nil {
		h.writeError(w, r, err)
		return
	}
	objects := make([]UploadObjectRequest, 0, len(req.Objects))
	for _, o := range req.Objects {
		if o.SizeBytes > maxGrantObjectBytes {
			h.writeError(w, r, writeTooLarge(o.Key))
			return
		}
		objects = append(objects, UploadObjectRequest{Key: o.Key, SizeBytes: o.SizeBytes})
	}
	grants, err := h.grants.IssueUploadGrants(r.Context(), cred.AgentID, jobID, objects)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	if grants == nil {
		grants = []ObjectGrant{}
	}
	writeJSON(w, http.StatusOK, uploadGrantsResponse{Grants: grants})
}

// handleMultipartInit starts a multipart upload for one job's object and
// returns the FULL part-URL list (part i uses part_urls[i-1]).
// POST /api/agent/jobs/:id/multipart/init
func (h *handler) handleMultipartInit(w http.ResponseWriter, r *http.Request) {
	cred := credentialFrom(r.Context())
	if cred == nil {
		h.writeError(w, r, agentuc.ErrUnauthorized)
		return
	}
	if h.grants == nil {
		h.writeError(w, r, errNotConfigured)
		return
	}
	jobID, err := requireJobID(r)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	var req multipartInitRequest
	if err := decodeJSON(r, &req); err != nil {
		h.writeError(w, r, err)
		return
	}
	if err := h.check(req); err != nil {
		h.writeError(w, r, err)
		return
	}
	if req.SizeBytes > maxGrantObjectBytes {
		h.writeError(w, r, writeTooLarge(req.Key))
		return
	}
	key, uploadID, partURLs, err := h.grants.InitMultipartUpload(r.Context(), cred.AgentID, jobID, req.Key, req.SizeBytes, req.PartSize)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	if partURLs == nil {
		partURLs = []string{}
	}
	writeJSON(w, http.StatusOK, multipartInitResponse{UploadID: uploadID, Key: key, PartURLs: partURLs})
}

// handleMultipartComplete finishes a multipart upload.
// POST /api/agent/jobs/:id/multipart/complete
func (h *handler) handleMultipartComplete(w http.ResponseWriter, r *http.Request) {
	cred := credentialFrom(r.Context())
	if cred == nil {
		h.writeError(w, r, agentuc.ErrUnauthorized)
		return
	}
	if h.grants == nil {
		h.writeError(w, r, errNotConfigured)
		return
	}
	jobID, err := requireJobID(r)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	var req multipartCompleteRequest
	if err := decodeJSON(r, &req); err != nil {
		h.writeError(w, r, err)
		return
	}
	if err := h.check(req); err != nil {
		h.writeError(w, r, err)
		return
	}
	parts := make([]MultipartPart, 0, len(req.Parts))
	for _, p := range req.Parts {
		if err := checkPartChecksum(p.Number, p.SHA256, p.SizeBytes); err != nil {
			h.writeError(w, r, err)
			return
		}
		parts = append(parts, MultipartPart{PartNumber: p.Number, ETag: p.ETag, SHA256: p.SHA256, SizeBytes: p.SizeBytes})
	}
	if err := h.grants.CompleteMultipartUpload(r.Context(), cred.AgentID, jobID, req.Key, req.UploadID, parts); err != nil {
		h.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, multipartDoneResponse{OK: true, Key: req.Key})
}

// handleMultipartAbort cancels an in-flight multipart upload.
// POST /api/agent/jobs/:id/multipart/abort
func (h *handler) handleMultipartAbort(w http.ResponseWriter, r *http.Request) {
	cred := credentialFrom(r.Context())
	if cred == nil {
		h.writeError(w, r, agentuc.ErrUnauthorized)
		return
	}
	if h.grants == nil {
		h.writeError(w, r, errNotConfigured)
		return
	}
	jobID, err := requireJobID(r)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	var req multipartAbortRequest
	if err := decodeJSON(r, &req); err != nil {
		h.writeError(w, r, err)
		return
	}
	if err := h.check(req); err != nil {
		h.writeError(w, r, err)
		return
	}
	if err := h.grants.AbortMultipartUpload(r.Context(), cred.AgentID, jobID, req.Key, req.UploadID); err != nil {
		h.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, multipartDoneResponse{OK: true, Key: req.Key})
}
