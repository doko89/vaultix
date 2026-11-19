package s3

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// fakeS3 emulates the S3 REST surface this client uses: HEAD/DELETE object,
// HEAD bucket, and the multipart upload flow. Tests drive failure injection
// through fail and retryAfter.
type fakeObject struct {
	body         []byte
	etag         string // bare value, served quoted
	sha256b64    string
	metaChecksum string
}

type fakeUpload struct {
	key       string
	parts     map[int]string
	aborted   bool
	completed bool
}

type fakeS3 struct {
	t          *testing.T
	mu         sync.Mutex
	objects    map[string]*fakeObject // "bucket/key" -> object
	uploads    map[string]*fakeUpload // uploadID -> upload
	uploadSeq  int
	fail       map[string]int // "METHOD path" -> remaining 500s
	retryAfter string         // applied to 500 responses when set
	authGaps   []string       // requests missing SigV4 auth material
	hits       []string
	server     *httptest.Server
}

func newFakeS3(t *testing.T) *fakeS3 {
	t.Helper()
	f := &fakeS3{
		t:       t,
		objects: map[string]*fakeObject{},
		uploads: map[string]*fakeUpload{},
		fail:    map[string]int{},
	}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeS3) putObject(bucket, key string, body []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	sum := sha256.Sum256(body)
	f.objects[bucket+"/"+key] = &fakeObject{
		body:      body,
		etag:      hex.EncodeToString(sum[:]),
		sha256b64: base64.StdEncoding.EncodeToString(sum[:]),
	}
}

func (f *fakeS3) hitCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.hits)
}

func (f *fakeS3) authGapsSnapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.authGaps...)
}

func (f *fakeS3) getUpload(id string) (fakeUpload, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.uploads[id]
	if !ok {
		return fakeUpload{}, false
	}
	return *u, true
}

func (f *fakeS3) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.hits = append(f.hits, r.Method+" "+r.URL.Path)
	if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256") {
		f.authGaps = append(f.authGaps, r.Method+" "+r.URL.Path)
	}
	remaining := f.fail[r.Method+" "+r.URL.Path]
	if remaining > 0 {
		f.fail[r.Method+" "+r.URL.Path] = remaining - 1
		retryAfter := f.retryAfter
		f.mu.Unlock()
		if retryAfter != "" {
			w.Header().Set("Retry-After", retryAfter)
		}
		http.Error(w, "injected failure", http.StatusInternalServerError)
		return
	}
	f.mu.Unlock()

	trimmed := strings.TrimPrefix(r.URL.Path, "/")
	bucket, key, hasKey := strings.Cut(trimmed, "/")
	if bucket == "" {
		http.Error(w, "missing bucket", http.StatusBadRequest)
		return
	}
	q := r.URL.Query()
	switch {
	case !hasKey || key == "":
		// Bucket-level: HEAD Bucket (ping).
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.Error(w, "unsupported bucket call", http.StatusBadRequest)
		return
	case r.Method == http.MethodPost && hasQueryKey(q, "uploads"):
		f.initiate(w, r, bucket, key)
		return
	case r.Method == http.MethodPost && q.Get("uploadId") != "":
		f.complete(w, r, q.Get("uploadId"))
		return
	case r.Method == http.MethodDelete && q.Get("uploadId") != "":
		f.abort(w, q.Get("uploadId"))
		return
	}

	switch r.Method {
	case http.MethodHead:
		f.mu.Lock()
		obj, ok := f.objects[bucket+"/"+key]
		f.mu.Unlock()
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(obj.body)))
		w.Header().Set("ETag", `"`+obj.etag+`"`)
		if obj.sha256b64 != "" {
			w.Header().Set("x-amz-checksum-sha256", obj.sha256b64)
		}
		if obj.metaChecksum != "" {
			w.Header().Set("x-amz-meta-checksum", obj.metaChecksum)
		}
		w.WriteHeader(http.StatusOK)
	case http.MethodDelete:
		f.mu.Lock()
		_, ok := f.objects[bucket+"/"+key]
		if ok {
			delete(f.objects, bucket+"/"+key)
		}
		f.mu.Unlock()
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "unsupported method", http.StatusBadRequest)
	}
}

func (f *fakeS3) initiate(w http.ResponseWriter, _ *http.Request, bucket, key string) {
	f.mu.Lock()
	f.uploadSeq++
	id := fmt.Sprintf("upload-%d", f.uploadSeq)
	f.uploads[id] = &fakeUpload{key: bucket + "/" + key, parts: map[int]string{}}
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, `<InitiateMultipartUploadResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Bucket>%s</Bucket><Key>%s</Key><UploadId>%s</UploadId></InitiateMultipartUploadResult>`, bucket, key, id)
}

func (f *fakeS3) complete(w http.ResponseWriter, r *http.Request, id string) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	var parsed struct {
		XMLName xml.Name `xml:"CompleteMultipartUpload"`
		Parts   []struct {
			Number int    `xml:"PartNumber"`
			ETag   string `xml:"ETag"`
		} `xml:"Part"`
	}
	_ = xml.Unmarshal(body, &parsed)
	f.mu.Lock()
	u, ok := f.uploads[id]
	if ok {
		u.completed = true
		for _, p := range parsed.Parts {
			u.parts[p.Number] = p.ETag
		}
	}
	f.mu.Unlock()
	if !ok {
		http.Error(w, "no such upload", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, `<CompleteMultipartUploadResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><ETag>"complete-%s"</ETag></CompleteMultipartUploadResult>`, id)
}

func (f *fakeS3) abort(w http.ResponseWriter, id string) {
	f.mu.Lock()
	u, ok := f.uploads[id]
	if ok {
		u.aborted = true
	}
	f.mu.Unlock()
	if !ok {
		http.Error(w, "no such upload", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func hasQueryKey(q url.Values, key string) bool {
	_, ok := q[key]
	return ok
}
