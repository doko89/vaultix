package s3

import (
	"fmt"
	"strings"
)

// jobsDir is the top-level key segment isolating one job's objects from
// another's. Every grant-scoped key has the form:
//
//	<prefix>/jobs/<jobID>/<object>
//
// with <prefix> empty when the destination has none.
const jobsDir = "jobs"

// ScopeKey builds the canonical object key for one job's object. It rejects
// empty job IDs, empty object names, and any traversal attempt.
func ScopeKey(prefix, jobID, object string) (string, error) {
	if err := validJobID(jobID); err != nil {
		return "", err
	}
	if err := validObjectName(object); err != nil {
		return "", err
	}
	prefix = strings.Trim(prefix, "/")
	if prefix == "" {
		return jobsDir + "/" + jobID + "/" + object, nil
	}
	if err := validPrefix(prefix); err != nil {
		return "", err
	}
	return prefix + "/" + jobsDir + "/" + jobID + "/" + object, nil
}

// ValidateScopedKey reports whether key addresses an object owned by jobID
// under prefix. A grant minted for job A therefore cannot address job B's
// paths: the caller's job binding is checked on every presign call.
func ValidateScopedKey(prefix, jobID, key string) error {
	if err := validJobID(jobID); err != nil {
		return err
	}
	if err := validKey(key); err != nil {
		return err
	}
	dir := scopeDir(prefix, jobID)
	if strings.HasPrefix(key, dir+"/") && len(key) > len(dir)+1 {
		return nil
	}
	return fmt.Errorf("s3: object key %q is outside job %q scope", key, jobID)
}

// ValidateJobScopedShape reports whether key has the jobs/<id>/<object> shape
// under prefix without binding it to a specific job. It is the port-level
// defense-in-depth check used where the authorized job is unknown; the
// use-case layer additionally binds each grant to its job.
func ValidateJobScopedShape(prefix, key string) error {
	if err := validKey(key); err != nil {
		return err
	}
	base := jobsDir
	if p := strings.Trim(prefix, "/"); p != "" {
		if err := validPrefix(p); err != nil {
			return err
		}
		base = p + "/" + jobsDir
	}
	rest, ok := strings.CutPrefix(key, base+"/")
	if !ok || rest == "" {
		return fmt.Errorf("s3: object key %q is not job-scoped", key)
	}
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return fmt.Errorf("s3: object key %q is not job-scoped", key)
	}
	if parts[0] == "." || parts[0] == ".." {
		return fmt.Errorf("s3: object key %q is not job-scoped", key)
	}
	return nil
}

// scopeDir returns the key prefix owned by one job.
func scopeDir(prefix, jobID string) string {
	if p := strings.Trim(prefix, "/"); p != "" {
		return p + "/" + jobsDir + "/" + jobID
	}
	return jobsDir + "/" + jobID
}

func validJobID(jobID string) error {
	if strings.TrimSpace(jobID) == "" {
		return fmt.Errorf("s3: job id is required")
	}
	if strings.ContainsAny(jobID, "/\\") || jobID == "." || jobID == ".." {
		return fmt.Errorf("s3: invalid job id %q", jobID)
	}
	return nil
}

func validObjectName(object string) error {
	if object == "" || object[0] == '/' || strings.Contains(object, "\\") {
		return fmt.Errorf("s3: invalid object name %q", object)
	}
	for _, seg := range strings.Split(object, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return fmt.Errorf("s3: invalid object name %q", object)
		}
	}
	return nil
}

func validKey(key string) error {
	if key == "" || key[0] == '/' || strings.Contains(key, "\\") {
		return fmt.Errorf("s3: invalid object key %q", key)
	}
	for _, seg := range strings.Split(key, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return fmt.Errorf("s3: invalid object key %q", key)
		}
	}
	return nil
}

func validPrefix(prefix string) error {
	for _, seg := range strings.Split(prefix, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return fmt.Errorf("s3: invalid prefix %q", prefix)
		}
	}
	return nil
}
