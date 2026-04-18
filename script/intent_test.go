package script

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIntentIDIsUnique(t *testing.T) {
	a := newIntentID()
	b := newIntentID()
	assert.NotEqual(t, a, b)
	assert.NotZero(t, a)
}

func TestIntentTypesImplementInterface(t *testing.T) {
	var _ Intent = QuitIntent{}
	var _ Intent = PushSelectedIntent{Confirm: true}
	var _ Intent = KillSelectedIntent{Confirm: true}
	var _ Intent = CheckoutIntent{Confirm: true, Help: true}
	var _ Intent = ResumeIntent{}
	var _ Intent = NewInstanceIntent{Prompt: true}
	var _ Intent = ShowHelpIntent{}
	var _ Intent = WorkspacePickerIntent{}
	var _ Intent = InlineAttachIntent{Pane: AttachPaneAgent}
	var _ Intent = FullscreenAttachIntent{Pane: AttachPaneTerminal}
	var _ Intent = QuickInputIntent{Pane: AttachPaneAgent}
}
