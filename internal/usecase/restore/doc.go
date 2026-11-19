// Package restore plans and executes restores from verified snapshots only
// (PRD §7.13): file/dir, database (postgres/mysql), volume, and
// stack-with-explicit-order restores behind plan → confirm → execute with
// single-use confirmation tokens.
package restore
