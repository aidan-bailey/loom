package transcript

// stepAt is where a step sits in the turns being built.
type stepAt struct{ turn, item, step int }

// builder folds entries, in file order, into turns.
type builder struct {
	start   int64 // the first line read: a partial turn's ID
	turns   []Turn
	steps   map[string]stepAt
	skipped int
}

// build folds entries (in file order, the first at start) into turns, and
// counts the lines it could not read.
func build(entries []entry, start int64) ([]Turn, int) {
	b := builder{start: start, steps: map[string]stepAt{}}
	for _, e := range entries {
		b.add(e)
	}
	b.count()
	return b.turns, b.skipped
}

func (b *builder) add(e entry) {
	if e.bad {
		b.skipped++
		return
	}
	if e.opens {
		if e.compact && b.repeatsCompact() {
			return
		}
		b.turns = append(b.turns, e.turn)
		return
	}
	for _, p := range e.parts {
		b.addPart(e, p)
	}
}

// repeatsCompact reports whether a /compact command record repeats the
// current turn: Claude writes a manual /compact as a plain /compact before
// its compact boundary, and again as a command record after the summary.
func (b *builder) repeatsCompact() bool {
	if len(b.turns) == 0 {
		return false
	}
	t := b.turns[len(b.turns)-1]
	if t.Partial || t.Text != "/compact" {
		return false
	}
	for _, it := range t.Items {
		if it.Kind == KindCompacted {
			return true
		}
	}
	return false
}

func (b *builder) addPart(e entry, p part) {
	if p.kind == partResult {
		if at, ok := b.steps[p.step.ID]; ok {
			b.turns[at.turn].Items[at.item].Steps[at.step].State = p.step.State
		}
		return
	}
	if len(b.turns) == 0 {
		b.turns = append(b.turns, Turn{ID: b.start, At: e.at, Partial: true})
	}
	ti := len(b.turns) - 1
	t := &b.turns[ti]
	if p.kind != partStep {
		t.Items = append(t.Items, Item{Kind: p.kind, At: e.at, Text: p.text, Cut: p.cut, Error: p.error})
		return
	}
	if n := len(t.Items); n == 0 || t.Items[n-1].Kind != KindSteps {
		t.Items = append(t.Items, Item{Kind: KindSteps, At: e.at})
	}
	ii := len(t.Items) - 1
	t.Items[ii].Steps = append(t.Items[ii].Steps, p.step)
	if p.step.ID != "" {
		b.steps[p.step.ID] = stepAt{ti, ii, len(t.Items[ii].Steps) - 1}
	}
}

// count totals each steps item, its failures included, and keeps its
// newest maxSteps steps.
func (b *builder) count() {
	for ti := range b.turns {
		for ii := range b.turns[ti].Items {
			it := &b.turns[ti].Items[ii]
			if it.Kind != KindSteps {
				continue
			}
			it.Total = len(it.Steps)
			for _, s := range it.Steps {
				if s.State == StateFailed {
					it.Failed++
				}
			}
			if len(it.Steps) > maxSteps {
				it.Steps = append([]Step(nil), it.Steps[len(it.Steps)-maxSteps:]...)
			}
		}
	}
}
