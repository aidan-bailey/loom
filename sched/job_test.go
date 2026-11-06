package sched

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// submitFixture is a submission that passes Validate; a test breaks one field
// of it.
func submitFixture() Submit {
	return Submit{
		Argv:        []string{"cargo", "test"},
		Cwd:         "/work/tree",
		Description: "tests",
		Tail:        20,
	}
}

func TestState_Finished(t *testing.T) {
	finished := map[State]bool{
		Queued: false, Running: false, Done: true, Failed: true, Cancelled: true, Lost: true,
	}
	for state, want := range finished {
		assert.Equal(t, want, state.Finished(), string(state))
	}
	assert.False(t, State("").Finished(), "a state this build does not know has not ended")
}

func TestClaim_Need(t *testing.T) {
	caps := Caps{CPUs: 14, MemoryGB: 22, IOJobs: 2}
	tests := []struct {
		name  string
		claim Claim
		caps  Caps
		want  Need
	}{
		{"plain", Claim{CPUs: 4, MemoryGB: 6}, caps, Need{CPUs: 4, MemoryGB: 6}},
		{"io takes one slot", Claim{CPUs: 2, MemoryGB: 3, IO: true}, caps, Need{CPUs: 2, MemoryGB: 3, IOSlots: 1}},
		{"exclusive takes every cap", Claim{Exclusive: true}, caps, Need{CPUs: 14, MemoryGB: 22, IOSlots: 2}},
		{"exclusive follows a cap edit", Claim{Exclusive: true}, Caps{CPUs: 8, MemoryGB: 10, IOJobs: 1}, Need{CPUs: 8, MemoryGB: 10, IOSlots: 1}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.claim.Need(tc.caps))
		})
	}
}

func TestSubmit_Validate(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Submit)
		want string // the error text; "" for none
	}{
		{"a valid submission", func(s *Submit) {}, ""},
		{"the folder need not exist", func(s *Submit) { s.Cwd = "/no/such/folder" }, ""},
		{"a description is trimmed", func(s *Submit) { s.Description = "  x  " }, ""},
		{"an empty description", func(s *Submit) { s.Description = "" }, "the description is empty"},
		{"a blank description", func(s *Submit) { s.Description = " \t\n" }, "the description is empty"},
		{"200 characters", func(s *Submit) { s.Description = strings.Repeat("é", 200) }, ""},
		{"200 characters between blanks", func(s *Submit) { s.Description = "  " + strings.Repeat("x", 200) + "  " }, ""},
		{"201 characters", func(s *Submit) { s.Description = strings.Repeat("é", 201) }, "the description is 201 characters: at most 200"},
		{"no command", func(s *Submit) { s.Argv = nil }, "no command to run"},
		{"a command of 64 KiB", func(s *Submit) { s.Argv = []string{strings.Repeat("a", 65536)} }, ""},
		{"a command of 64 KiB and a byte", func(s *Submit) { s.Argv = []string{"a", strings.Repeat("a", 65536)} }, "the command is 65537 bytes: at most 65536"},
		{"an environment of 1 MiB", func(s *Submit) { s.Env = []string{strings.Repeat("a", 1<<20)} }, ""},
		{"an environment of 1 MiB and a byte", func(s *Submit) { s.Env = []string{"a", strings.Repeat("a", 1<<20)} }, "the environment is 1048577 bytes: at most 1048576"},
		{"tail 0", func(s *Submit) { s.Tail = 0 }, ""},
		{"tail 200", func(s *Submit) { s.Tail = 200 }, ""},
		{"tail 201", func(s *Submit) { s.Tail = 201 }, "--tail is 201: it must be 0–200"},
		{"tail -1", func(s *Submit) { s.Tail = -1 }, "--tail is -1: it must be 0–200"},
		{"cpus 0 takes the default claim's", func(s *Submit) { s.Claim.CPUs = 0 }, ""},
		{"cpus -1", func(s *Submit) { s.Claim.CPUs = -1 }, "--cpus is -1: it must be at least 1"},
		{"mem 0 takes the default claim's", func(s *Submit) { s.Claim.MemoryGB = 0 }, ""},
		{"mem -3", func(s *Submit) { s.Claim.MemoryGB = -3 }, "--mem is -3: it must be at least 1"},
		{"exclusive alone", func(s *Submit) { s.Claim = Claim{Exclusive: true} }, ""},
		{"exclusive with cpus", func(s *Submit) { s.Claim = Claim{Exclusive: true, CPUs: 2} }, "--exclusive claims everything: drop --cpus, --mem and --io"},
		{"exclusive with mem", func(s *Submit) { s.Claim = Claim{Exclusive: true, MemoryGB: 2} }, "--exclusive claims everything: drop --cpus, --mem and --io"},
		{"exclusive with io", func(s *Submit) { s.Claim = Claim{Exclusive: true, IO: true} }, "--exclusive claims everything: drop --cpus, --mem and --io"},
		{"a relative folder", func(s *Submit) { s.Cwd = "work/tree" }, "the folder work/tree is not an absolute path"},
		{"no folder", func(s *Submit) { s.Cwd = "" }, "the folder  is not an absolute path"},
		{"a bare agent job", func(s *Submit) { s.Kind = KindAgent }, ""},
		{"an agent job with cpus", func(s *Submit) { s.Kind, s.Claim = KindAgent, Claim{CPUs: 1} }, "an agent job claims nothing and runs after nothing"},
		{"an agent job with io", func(s *Submit) { s.Kind, s.Claim = KindAgent, Claim{IO: true} }, "an agent job claims nothing and runs after nothing"},
		{"an agent job, exclusive", func(s *Submit) { s.Kind, s.Claim = KindAgent, Claim{Exclusive: true} }, "an agent job claims nothing and runs after nothing"},
		{"an agent job after another", func(s *Submit) { s.Kind, s.After = KindAgent, 3 }, "an agent job claims nothing and runs after nothing"},
		{"a bare run job", func(s *Submit) { s.Kind = KindRun }, ""},
		{"a run job with cpus", func(s *Submit) { s.Kind, s.Claim = KindRun, Claim{CPUs: 1} }, "a run job claims nothing and runs after nothing"},
		{"a run job with io", func(s *Submit) { s.Kind, s.Claim = KindRun, Claim{IO: true} }, "a run job claims nothing and runs after nothing"},
		{"a run job, exclusive", func(s *Submit) { s.Kind, s.Claim = KindRun, Claim{Exclusive: true} }, "a run job claims nothing and runs after nothing"},
		{"a run job after another", func(s *Submit) { s.Kind, s.After = KindRun, 3 }, "a run job claims nothing and runs after nothing"},
		{"an unknown kind", func(s *Submit) { s.Kind = "x" }, `unknown job kind "x"`},
		{"a port lock", func(s *Submit) { s.Locks = []string{"port:4300"} }, ""},
		{"locks of every allowed character", func(s *Submit) { s.Locks = []string{"a-Z_0.9:/x", "port:4310"} }, ""},
		{"8 locks", func(s *Submit) { s.Locks = []string{"a", "b", "c", "d", "e", "f", "g", "h"} }, ""},
		{"9 locks", func(s *Submit) { s.Locks = []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"} }, "9 locks: at most 8"},
		{"an empty lock", func(s *Submit) { s.Locks = []string{""} }, `--lock "": a name is 1–64 of A–Z, a–z, 0–9 and . _ : / -`},
		{"a lock of 64 characters", func(s *Submit) { s.Locks = []string{strings.Repeat("x", 64)} }, ""},
		{"a lock of 65 characters", func(s *Submit) { s.Locks = []string{strings.Repeat("x", 65)} }, `--lock "` + strings.Repeat("x", 65) + `": a name is 1–64 of A–Z, a–z, 0–9 and . _ : / -`},
		{"a lock with a space", func(s *Submit) { s.Locks = []string{"port 4300"} }, `--lock "port 4300": a name is 1–64 of A–Z, a–z, 0–9 and . _ : / -`},
		{"a lock twice", func(s *Submit) { s.Locks = []string{"port:4300", "port:4300"} }, "--lock port:4300 is given twice"},
		{"an agent job with a lock", func(s *Submit) { s.Kind, s.Locks = KindAgent, []string{"port:4300"} }, "an agent job takes no lock"},
		{"a run job with a lock", func(s *Submit) { s.Kind, s.Locks = KindRun, []string{"port:4300"} }, "a run job takes no lock"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := submitFixture()
			tc.edit(&s)
			err := s.Validate()
			if tc.want == "" {
				assert.NoError(t, err)
				return
			}
			assert.EqualError(t, err, tc.want)
		})
	}
}

// TestSubmit_ValidateReportsTheFirstFault: a submission with every fault is
// refused for the first one in the order the texts are listed, and each fix
// brings up the next.
func TestSubmit_ValidateReportsTheFirstFault(t *testing.T) {
	s := Submit{Cwd: "rel", Tail: 500, Claim: Claim{CPUs: -1, MemoryGB: -1, IO: true, Exclusive: true}}
	wants := []string{
		"the description is empty",
		"no command to run",
		"--tail is 500: it must be 0–200",
		"--cpus is -1: it must be at least 1",
		"--mem is -1: it must be at least 1",
		"--exclusive claims everything: drop --cpus, --mem and --io",
		"the folder rel is not an absolute path",
	}
	fixes := []func(*Submit){
		func(s *Submit) { s.Description = "d" },
		func(s *Submit) { s.Argv = []string{"x"} },
		func(s *Submit) { s.Tail = 0 },
		func(s *Submit) { s.Claim.CPUs = 0 },
		func(s *Submit) { s.Claim.MemoryGB = 0 },
		func(s *Submit) { s.Claim.IO = false },
		func(s *Submit) { s.Cwd = "/ok" },
	}
	for i, want := range wants {
		require.EqualError(t, s.Validate(), want, "fault %d", i)
		fixes[i](&s)
	}
	assert.NoError(t, s.Validate())
}

func TestCheckClaim(t *testing.T) {
	p := Policy{Caps: Caps{CPUs: 14, MemoryGB: 22, IOJobs: 2}}
	tests := []struct {
		name  string
		claim Claim
		want  string // the error text; "" for none
	}{
		{"at the cores cap", Claim{CPUs: 14, MemoryGB: 1}, ""},
		{"over the cores cap", Claim{CPUs: 15, MemoryGB: 1}, "claims 15 cores; this host's cap is 14"},
		{"at the memory cap", Claim{CPUs: 1, MemoryGB: 22}, ""},
		{"over the memory cap", Claim{CPUs: 1, MemoryGB: 23}, "claims 23 GB; this host's cap is 22 GB"},
		{"over both reports the cores", Claim{CPUs: 32, MemoryGB: 64}, "claims 32 cores; this host's cap is 14"},
		{"an I/O job", Claim{CPUs: 2, MemoryGB: 2, IO: true}, ""},
		{"exclusive claims what the caps are", Claim{Exclusive: true}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckClaim(tc.claim, p)
			if tc.want == "" {
				assert.NoError(t, err)
				return
			}
			assert.EqualError(t, err, tc.want)
		})
	}
}

// TestClaim_ExceedsAgreesWithCheckClaim pins that the over-cap test is written
// once: Admit's skip and the submission check cannot disagree.
func TestClaim_ExceedsAgreesWithCheckClaim(t *testing.T) {
	caps := Caps{CPUs: 14, MemoryGB: 22, IOJobs: 2}
	p := Policy{Caps: caps}
	tests := []struct {
		name  string
		claim Claim
		over  bool
	}{
		{"zero claim", Claim{}, false},
		{"at both caps", Claim{CPUs: 14, MemoryGB: 22}, false},
		{"cores one over", Claim{CPUs: 15, MemoryGB: 22}, true},
		{"memory one over", Claim{CPUs: 14, MemoryGB: 23}, true},
		{"both over", Claim{CPUs: 15, MemoryGB: 23}, true},
		{"io at the caps", Claim{CPUs: 14, MemoryGB: 22, IO: true}, false},
		{"exclusive", Claim{Exclusive: true}, false},
		{"exclusive with stray numbers", Claim{Exclusive: true, CPUs: 99, MemoryGB: 99}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.over, tc.claim.exceeds(caps))
			assert.Equal(t, tc.claim.exceeds(caps), CheckClaim(tc.claim, p) != nil)
		})
	}
}

func TestClaim_String(t *testing.T) {
	tests := []struct {
		claim Claim
		want  string
	}{
		{Claim{CPUs: 4, MemoryGB: 6}, "4c/6G"},
		{Claim{CPUs: 4, MemoryGB: 6, IO: true}, "4c/6G io"},
		{Claim{CPUs: 1, MemoryGB: 1}, "1c/1G"},
		{Claim{}, "0c/0G"},
		{Claim{Exclusive: true}, "exclusive"},
		{Claim{Exclusive: true, IO: true, CPUs: 2}, "exclusive"},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, tc.claim.String())
	}
}

func TestClaim_Spared(t *testing.T) {
	assert.Equal(t, "4+2c/3G", Claim{CPUs: 6, MemoryGB: 3}.Spared(Claim{CPUs: 2, MemoryGB: 1}))
	assert.Equal(t, "4+2c/3G io", Claim{CPUs: 6, MemoryGB: 3, IO: true}.Spared(Claim{CPUs: 2, MemoryGB: 1}))
	assert.Equal(t, "4c/6G", Claim{CPUs: 4, MemoryGB: 6}.Spared(Claim{}))
	assert.Equal(t, "exclusive", Claim{Exclusive: true}.Spared(Claim{}))
}

func TestState_HeldWaitsAndHasNotEnded(t *testing.T) {
	assert.False(t, Held.Finished(), "a held job waits for its prerequisite")
	for state, want := range map[State]bool{
		Held: true, Queued: true, Running: false, Done: false, Failed: false, Cancelled: false, Lost: false,
	} {
		assert.Equal(t, want, state.Waiting(), string(state))
	}
}

func TestRecord_QueuedSince(t *testing.T) {
	t0 := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	assert.Equal(t, t0, Record{Submitted: t0}.QueuedSince(), "a record from an earlier build has no Ready")
	assert.Equal(t, t0.Add(time.Minute), Record{Submitted: t0, Ready: t0.Add(time.Minute)}.QueuedSince(),
		"a released job joined the queue at its release")
}

func TestSubmit_ValidateRefusesANegativeAfter(t *testing.T) {
	s := Submit{Argv: []string{"true"}, Cwd: "/work", Description: "x", Tail: 20, After: -2}
	assert.EqualError(t, s.Validate(), "--after is -2: it must be a job ID")
	s.After = 0
	assert.NoError(t, s.Validate(), "0 is none")
	s.After = 14
	assert.NoError(t, s.Validate())
}
