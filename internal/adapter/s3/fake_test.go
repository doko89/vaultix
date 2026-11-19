package s3

import (
	"crypto/md5"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// fakeS3 emulates the S3 REST surface this client uses: HEAD/GET/PUT/DELETE
// object, HEAD bucket, the multipart upload flow (initiate, part PUT,
// complete with assembly, abort), and the checksum-stamp copies (CopyObject
// self-copy, UploadPartCopy). Tests drive failure injection through fail
// and retryAfter.
type fakeObject struct {
	body         []byte
	etag         string // bare value, served quoted
	sha256b64    string
	metaChecksum string
}

type fakeUpload struct {
	key        string
	parts      map[int]string
	partsData  map[int][]byte
	pendingMet string // x-amz-meta-checksum captured at initiate (copy tier)
	aborted    bool
	completed  bool
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

// putRawObject stores an object verbatim (for ETag-only / legacy shapes).
func (f *fakeS3) putRawObject(bucket, key string, obj fakeObject) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := obj
	f.objects[bucket+"/"+key] = &cp
}

// storePart stages part bytes directly, as if PUT through a presigned URL.
func (f *fakeS3) storePart(uploadID string, partNumber int, body []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.uploads[uploadID]
	if !ok {
		return
	}
	u.partsData[partNumber] = append([]byte(nil), body...)
}

// mutatePart rewrites staged part bytes (tamper/truncation injection).
func (f *fakeS3) mutatePart(uploadID string, partNumber int, fn func([]byte) []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if u, ok := f.uploads[uploadID]; ok {
		u.partsData[partNumber] = fn(u.partsData[partNumber])
	}
}

// getObject returns a copy of a stored object.
func (f *fakeS3) getObject(bucket, key string) (fakeObject, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	obj, ok := f.objects[bucket+"/"+key]
	if !ok {
		return fakeObject{}, false
	}
	return *obj, true
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
	copySource := r.Header.Get("x-amz-copy-source")
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
	case r.Method == http.MethodPut && copySource != "" && q.Get("uploadId") != "" && q.Get("partNumber") != "":
		f.uploadPartCopy(w, r, q.Get("uploadId"), q.Get("partNumber"), copySource)
		return
	case r.Method == http.MethodPut && copySource != "":
		f.copyObject(w, r, bucket, key, copySource)
		return
	case r.Method == http.MethodPut && q.Get("uploadId") != "" && q.Get("partNumber") != "":
		f.putPart(w, r, q.Get("uploadId"), q.Get("partNumber"))
		return
	case r.Method == http.MethodPut:
		f.putObjectHTTP(w, r, bucket, key)
		return
	case r.Method == http.MethodGet:
		f.getObjectHTTP(w, r, bucket, key)
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

func (f *fakeS3) initiate(w http.ResponseWriter, r *http.Request, bucket, key string) {
	f.mu.Lock()
	f.uploadSeq++
	id := fmt.Sprintf("upload-%d", f.uploadSeq)
	f.uploads[id] = &fakeUpload{
		key:        bucket + "/" + key,
		parts:      map[int]string{},
		partsData:  map[int][]byte{},
		pendingMet: strings.TrimSpace(r.Header.Get("x-amz-meta-checksum")),
	}
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
	if !ok {
		f.mu.Unlock()
		http.Error(w, "no such upload", http.StatusNotFound)
		return
	}
	u.completed = true
	for _, p := range parsed.Parts {
		u.parts[p.Number] = p.ETag
	}
	// Assemble the object from staged part bytes in part-number order, like
	// S3. A listed part with no staged bytes is an InvalidPart failure.
	numbers := make([]int, 0, len(parsed.Parts))
	for _, p := range parsed.Parts {
		numbers = append(numbers, p.Number)
	}
	slices.Sort(numbers)
	var assembled []byte
	partMD5s := make([][]byte, 0, len(numbers))
	for _, n := range numbers {
		data, ok := u.partsData[n]
		if !ok {
			f.mu.Unlock()
			http.Error(w, "invalid part", http.StatusBadRequest)
			return
		}
		assembled = append(assembled, data...)
		sum := md5.Sum(data)
		partMD5s = append(partMD5s, sum[:])
	}
	etag := multipartETag(partMD5s)
	f.objects[u.key] = &fakeObject{
		body:         assembled,
		etag:         etag,
		metaChecksum: u.pendingMet,
	}
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, `<CompleteMultipartUploadResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><ETag>"%s"</ETag></CompleteMultipartUploadResult>`, etag)
}

// multipartETag renders the S3 multipart ETag form: hex(md5(concat(md5 of
// each part))) + "-N". Deliberately never a content hash.
func multipartETag(partMD5s [][]byte) string {
	h := md5.New()
	for _, sum := range partMD5s {
		_, _ = h.Write(sum)
	}
	return hex.EncodeToString(h.Sum(nil)) + "-" + strconv.Itoa(len(partMD5s))
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

// putPart stores part bytes uploaded through a presigned part URL.
func (f *fakeS3) putPart(w http.ResponseWriter, r *http.Request, uploadID, partNumber string) {
	n, err := strconv.Atoi(partNumber)
	if err != nil || n < 1 || n > 10000 {
		http.Error(w, "invalid part number", http.StatusBadRequest)
		return
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 256<<20))
	f.mu.Lock()
	u, ok := f.uploads[uploadID]
	if ok && !u.aborted {
		u.partsData[n] = body
	}
	f.mu.Unlock()
	if !ok {
		http.Error(w, "no such upload", http.StatusNotFound)
		return
	}
	sum := md5.Sum(body)
	w.Header().Set("ETag", `"`+hex.EncodeToString(sum[:])+`"`)
	w.WriteHeader(http.StatusOK)
}

// putObjectHTTP stores a plain single-PUT object with its user metadata.
func (f *fakeS3) putObjectHTTP(w http.ResponseWriter, r *http.Request, bucket, key string) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 256<<20))
	sum := md5.Sum(body)
	f.mu.Lock()
	f.objects[bucket+"/"+key] = &fakeObject{
		body:         body,
		etag:         hex.EncodeToString(sum[:]),
		metaChecksum: strings.TrimSpace(r.Header.Get("x-amz-meta-checksum")),
	}
	f.mu.Unlock()
	w.Header().Set("ETag", `"`+hex.EncodeToString(sum[:])+`"`)
	w.WriteHeader(http.StatusOK)
}

// getObjectHTTP serves whole or ranged GETs.
func (f *fakeS3) getObjectHTTP(w http.ResponseWriter, r *http.Request, bucket, key string) {
	f.mu.Lock()
	obj, ok := f.objects[bucket+"/"+key]
	f.mu.Unlock()
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	rangeHeader := strings.TrimSpace(r.Header.Get("Range"))
	if rangeHeader == "" {
		w.Header().Set("Content-Length", strconv.Itoa(len(obj.body)))
		w.Header().Set("ETag", `"`+obj.etag+`"`)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(obj.body)
		return
	}
	start, end, ok := parseRangeHeader(rangeHeader, len(obj.body))
	if !ok {
		w.Header().Set("Content-Range", "bytes */"+strconv.Itoa(len(obj.body)))
		http.Error(w, "invalid range", http.StatusRequestedRangeNotSatisfiable)
		return
	}
	w.Header().Set("Content-Range", "bytes "+strconv.Itoa(start)+"-"+strconv.Itoa(end)+"/"+strconv.Itoa(len(obj.body)))
	w.Header().Set("Content-Length", strconv.Itoa(end-start+1))
	w.Header().Set("ETag", `"`+obj.etag+`"`)
	w.WriteHeader(http.StatusPartialContent)
	_, _ = w.Write(obj.body[start : end+1])
}

// parseRangeHeader parses a single "bytes=start-end" range.
func parseRangeHeader(v string, total int) (int, int, bool) {
	span, ok := strings.CutPrefix(v, "bytes=")
	if !ok {
		return 0, 0, false
	}
	s, e, ok := strings.Cut(span, "-")
	if !ok {
		return 0, 0, false
	}
	start, err1 := strconv.Atoi(strings.TrimSpace(s))
	end, err2 := strconv.Atoi(strings.TrimSpace(e))
	if err1 != nil || err2 != nil || start < 0 || end < start || start >= total {
		return 0, 0, false
	}
	if end >= total {
		end = total - 1
	}
	return start, end, true
}

// copyObject emulates a CopyObject self-copy with REPLACE metadata.
func (f *fakeS3) copyObject(w http.ResponseWriter, r *http.Request, bucket, key, source string) {
	srcBucket, srcKey, err := parseCopySource(source)
	if err != nil {
		http.Error(w, "invalid copy source", http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	src, ok := f.objects[srcBucket+"/"+srcKey]
	if !ok {
		f.mu.Unlock()
		http.Error(w, "copy source not found", http.StatusNotFound)
		return
	}
	body := append([]byte(nil), src.body...)
	meta := src.metaChecksum
	if strings.EqualFold(strings.TrimSpace(r.Header.Get("x-amz-metadata-directive")), "REPLACE") {
		meta = strings.TrimSpace(r.Header.Get("x-amz-meta-checksum"))
	}
	sum := md5.Sum(body)
	f.objects[bucket+"/"+key] = &fakeObject{body: body, etag: hex.EncodeToString(sum[:]), metaChecksum: meta}
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, `<CopyObjectResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><ETag>"%s"</ETag></CopyObjectResult>`, hex.EncodeToString(sum[:]))
}

// uploadPartCopy emulates UploadPartCopy: one source range into a copy-upload
// part.
func (f *fakeS3) uploadPartCopy(w http.ResponseWriter, r *http.Request, uploadID, partNumber, source string) {
	n, err := strconv.Atoi(partNumber)
	if err != nil || n < 1 || n > 10000 {
		http.Error(w, "invalid part number", http.StatusBadRequest)
		return
	}
	srcBucket, srcKey, err := parseCopySource(source)
	if err != nil {
		http.Error(w, "invalid copy source", http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	src, ok := f.objects[srcBucket+"/"+srcKey]
	u, uok := f.uploads[uploadID]
	if !ok || !uok {
		f.mu.Unlock()
		http.Error(w, "copy source or upload not found", http.StatusNotFound)
		return
	}
	data := src.body
	if rng := strings.TrimSpace(r.Header.Get("x-amz-copy-source-range")); rng != "" {
		start, end, ok := parseRangeHeader(rng, len(src.body))
		if !ok {
			f.mu.Unlock()
			http.Error(w, "invalid copy range", http.StatusRequestedRangeNotSatisfiable)
			return
		}
		data = append([]byte(nil), src.body[start:end+1]...)
	} else {
		data = append([]byte(nil), data...)
	}
	u.partsData[n] = data
	f.mu.Unlock()
	sum := md5.Sum(data)
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, `<CopyPartResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><ETag>"%s"</ETag></CopyPartResult>`, hex.EncodeToString(sum[:]))
}

func hasQueryKey(q url.Values, key string) bool {
	_, ok := q[key]
	return ok
}
