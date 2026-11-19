// Package schedule holds schedule use-case rules (PRD §7.9).
//
// The Service owns one-time and recurring schedules (hourly/daily/weekly
// plus a 5-field cron escape hatch), stored timezones, next-run computation,
// pause/resume, and admission (overlap prevention + per-agent/global
// concurrency limits). Manual triggers enqueue through the narrow
// JobEnqueuer interface and recurring fires dispatch through RecurringRunner;
// the jobs and scheduler tracks own the real implementations (see
// INTEGRATION notes on those interfaces).
package schedule
