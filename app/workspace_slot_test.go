package app

// focusSlots installs slots as h's open workspace tabs and focuses
// slots[focused], so the embedded focused slot is the same pointer as
// h.slots[focused] (the invariant checkSlotInvariant enforces).
func focusSlots(h *home, focused int, slots ...*workspaceSlot) {
	h.slots = slots
	h.focusedSlot = focused
	h.workspaceSlot = slots[focused]
}
