package copilot

// This file previously contained the fry-main-owned TickScheduler that
// managed the copilot's wake loop as a goroutine inside the fry process.
// That design was removed because the scheduler died when fry crashed,
// leaving the copilot blind to build failures.
//
// The copilot now manages its own schedule via CronCreate during the
// bootstrap prompt. It is a fully independent process that survives fry
// crashes and can detect and recover from them.
