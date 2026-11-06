# Run records

One file per supervised plan run, written by the manager when the run ends
(under `/flow:run`, by loom's run engine itself, whatever the run's end), at
`~/.loom/flow/records/<key>/<YYYY-MM-DD>-<plan stem without its leading date>.md`,
where `<key>` is what `loom flow conf key` prints for the repo (plan file names are
dated already, so the date is not written twice). That directory is no
repository, so nothing commits it there. When the repo's profile sets
`paths.records`, `/flow:supervise` also writes a copy there and commits that in
the repo. Records hold facts. Judging them is a
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
     none, and its record leaves the line out.

   - `- Ended:` and `- Engine:`, in a record loom's run engine wrote
     (`/flow:run`): how the run ended (`done`, `stopped` or `cancelled`), and
     `loom flow run (flow in loom part 3b)`. Its Cost is
     `loom flow costs run <run>`'s output, which prices the manager's sessions
     as `main` and each headless agent run by its role, and its Notes list
     every question the engine asked with its answer.

   Write the Definitions, Profile and Design stage lines in the header's label
   style, plain or bold, whichever the rest of that header uses.
2. **Cost**: the output of
   `loom flow costs session --since <start> --until <end>`,
   pasted whole, including its closing `<!-- agent-costs … -->` comment, which
   `loom flow costs records` reads for retros. A run that spanned more than one
   manager session (a usage-limit pause, then a resume) pastes one output per
   session, `loom flow costs session <session id> --since <start> --until <end>`,
   and `records` sums them into one run. The figures are API list prices; on a
   subscription they are relative weights, not a bill.
3. **Stories**: one row per story, copied from the supervisor's state file:

   | Story | Developer | Tier | Review | Commits | Status | Deviations | BLOCKED, up a tier | Escalated to manager | Review findings | Turn cap hit |
   |---|---|---|---|---|---|---|---|---|---|---|

   **Tier** is the story's `deep` or `light` and **Review** its `vouched` or
   `full`, as `loom flow check-plan` printed them and the supervisor's state file
   carries them.
4. **Notes**: facts worth keeping, such as answers the manager or owner gave
   and anything that surprised. No conclusions; those belong to a retro.

A retro file ends with
`<!-- flow-retro covered: ["<key>/<record file>", …] -->`, naming every record
it covered. The next retro gathers every record that no retro names.
