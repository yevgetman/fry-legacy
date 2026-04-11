package copilot

// This file previously contained the Supervisor type that watched for
// copilot manifest creation/deletion and managed the TickScheduler
// lifecycle inside the fry main process.
//
// The Supervisor was removed because the copilot now manages its own
// schedule via CronCreate. fry main no longer needs to supervise the
// copilot — it is a fully independent process.
