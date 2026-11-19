// Package backup resolves backup job targets from pinned policy versions,
// assembles snapshot manifests, and gates job success on full verification
// (PRD §7.5, §7.7, §7.8, §7.10, §7.11, §7.12).
//
// A backup job is NEVER reported successful unless its manifest and every
// required object pass verification (see VerifySnapshot).
package backup
