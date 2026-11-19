package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validJob() Job {
	return Job{
		ID: "job-1", AgentID: "agent-1",
		Policy: "daily", PolicyVersion: 3,
		Kind: JobKindFilesystem, Targets: []string{"/var/www"},
	}
}

func TestValidateJobAcceptsValid(t *testing.T) {
	require.NoError(t, ValidateJob(validJob(), "agent-1"))
}

func TestValidateJobRejectsMalformed(t *testing.T) {
	cases := map[string]func(*Job){
		"missing id":     func(j *Job) { j.ID = "" },
		"missing agent":  func(j *Job) { j.AgentID = "" },
		"missing policy": func(j *Job) { j.Policy = "" },
		"bad version":    func(j *Job) { j.PolicyVersion = 0 },
		"bad kind":       func(j *Job) { j.Kind = "nuke" },
		"no targets":     func(j *Job) { j.Targets = nil },
		"empty target":   func(j *Job) { j.Targets = []string{""} },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			j := validJob()
			mutate(&j)
			err := ValidateJob(j, "agent-1")
			require.Error(t, err)
			var verr *ValidationError
			require.ErrorAs(t, err, &verr)
		})
	}
}

func TestValidateJobRejectsUnauthorized(t *testing.T) {
	err := ValidateJob(validJob(), "agent-2")
	require.Error(t, err)
	var uerr *UnauthorizedError
	require.ErrorAs(t, err, &uerr)
	assert.Equal(t, "job-1", uerr.JobID)
}
