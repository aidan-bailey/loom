// Package sched is the work scheduler's logic and its runtime: what a job, a
// claim and the host's caps are, first-fit admission with a starvation guard,
// the jobs.json store and the per-job files, and the processes that host a
// running job (the detached shim, the lock probe, group signals, the usage
// sampler and host detection).
//
// The model (package core) owns the queue. It persists it under
// <globalDir>/jobs/, calls [Admit] on its loop, and starts and watches jobs
// through this package. Nothing here keeps state of its own, so the logic
// takes the host, the policy and the clock as arguments.
package sched
