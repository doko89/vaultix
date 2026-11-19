// Package notification holds notification use-case rules (PRD §7.16):
// alert evaluation for failed jobs and agent state transitions, delivery
// over webhook and SMTP senders, and rule administration for the
// notifications API. Persistence goes through domain.NotificationRepository
// (implemented by the repos track); delivery retries live in the senders.
package notification
