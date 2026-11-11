# Run records

One file per supervised plan run, written by loom's run engine (`/flow:run`)
when the run ends, whatever its end (a record from before 2026-11-11 may be a
`/flow:supervise` manager's, written by hand), at
`~/.loom/flow/records/<key>/<YYYY-MM-DD>-<plan stem without its leading date>.md`,
where `<key>` is what `loom flow conf key` prints for the repo (plan file names are
dated already, so the date is not written twice). That directory is no
repository, so nothing commits it there. When the repo's profile sets
`paths.records`, the engine also writes a copy there, which the manager
commits in the repo. Records hold facts. Judging them is a
retro's job: the owner starts one with `/flow:retro`, and it lands at
`records/retros/retro-<YYYY-MM-DD>.md` (when that file exists, `-2`, `-3`, … is
added after the date).

A record has four parts.

1. **Header**, a list:
   - `- Plan:` the plan's path, with its story count;
   - `- Spec:` the spec's path;
   - `- Commits:` the range `BASE_SHA..HEAD`, with its commit count;
   - `- Manager:` the manager's model and session id, or ids;
   - `- Start:` the run's start time, and `End:` its end time, UTC;
   - `- Definitions:` `loom flow roster <12 hex>`, as `loom flow conf roster`
     prints it at the start of the run: the first 12 hex digits of the SHA-256
     over the flow plugin's files in the loom build that launched the session
     (`flow.RosterID`). ` → loom flow roster <12 hex>` is appended only when it
     differs at the end of the run: the session relaunched on another build.
     Records written before flow moved into loom (2026-11-05) say
     `flow @ <short sha>[+dirty] roster <12 hex>`, claude-flow's commit and the
     hash of its `git ls-tree` of `agents skills home.md`. A record
     imported from a repo that ran its own roster says
     `<repo name> in-repo @ <BASE_SHA> roster <12 hex>`, hashing that repo's
     `.claude/agents` and its developers' habits skill under `.claude/skills`
     at `BASE_SHA` the same way;
   - `- Profile:` the profile's path and `sha256:` with the first 12 hex
     digits of its hash, as the `Profile:` line of
     `loom flow conf show --for supervisor` prints them;
   - `- Design stage:` the text after the label of the plan's
     `**Design stage:**` line, when the plan has one: `/flow:draft` writes it
     as it commits the plan, with its sessions, their window, their logged
     cost and the plan's length. A plan written without `/flow:draft` has
     none, and its record leaves the line out;
   - `- Part:` `N of M of <spec path>`, when the plan has a `**Part:**`
     line: the run is one part of an epic, and `loom flow costs epic <spec>`
     totals its parts.

   - `- Ended:` and `- Engine:`, in a record loom's run engine wrote
     (`/flow:run`): how the run ended (`done`, `stopped` or `cancelled`), and
     `loom flow run (flow in loom part 3b)`. Its Cost is
     `loom flow costs run <run>`'s output, which prices the manager's sessions
     as `main` and each headless agent run by its role, and its Notes open
     with the count of questions answered from the spec and by the owner
     (`loom flow run answer --from`), then list every question with its
     answer and where it came from.
     Its Notes end with a `Gates` line for each gate run, which names the
     failing steps and their verdicts when the run did not pass, and a `Disk`
     line giving the lowest free space at a gate run's end. The Cost section's
     agent table has an **Effort** column, read from each transcript: compare
     it with the agent's pin.

   Write the Definitions, Profile and Design stage lines in the header's label
   style, plain or bold, whichever the rest of that header uses.
2. **Cost**: in a record a manager wrote, the output of
   `loom flow costs session --since <start> --until <end>`,
   pasted whole, including its closing `<!-- agent-costs … -->` comment, which
   `loom flow costs records` reads for retros. A run that spanned more than one
   manager session (a usage-limit pause, then a resume) pastes one output per
   session, `loom flow costs session <session id> --since <start> --until <end>`,
   and `records` sums them into one run. The figures are API list prices; on a
   subscription they are relative weights, not a bill. loom's flow board on the
   phone prices a run as it goes with `loom flow costs run <run> --json` (the same
   figures, as one object) and the design session with
   `loom flow costs design <spec> --plan <plan>`; neither writes a record.
3. **Stories**: one row per story, from the run's state:

   | Story | Developer | Tier | Review | Commits | Status | Deviations | BLOCKED, up a tier | Escalated to manager | Review findings | Turn cap hit | Docs | Lessons |
   |---|---|---|---|---|---|---|---|---|---|---|---|---|

   **Tier** is the story's `deep` or `light` and **Review** its `vouched` or
   `full`, as `loom flow check-plan` printed them. **Docs** and **Lessons** count the `DOC:` and `LESSON:` lines
   of the story's doc pass, `—` for a run without one. Under `/flow:run`, a
   laned story's **Developer** ends `(lane)`, and the notes say how many of
   its commits were picked onto which head, and name any lane left.
4. **Notes**: facts worth keeping, such as answers the manager or owner gave
   and anything that surprised. No conclusions; those belong to a retro.

A retro file ends with
`<!-- flow-retro covered: ["<key>/<record file>", …] -->`, naming every record
it covered. The next retro gathers every record that no retro names.
