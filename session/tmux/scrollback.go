package tmux

// paneScrollbackLines is how many lines of scroll-back each pane's emulator
// keeps, the depth the TUI's preview scrolls to (tmux keeps its own history,
// history-limit, for a full-screen attach). x/vt stores a line as cells of
// about 118 bytes, some 15 KB for a full-width one, so its default of 10,000
// let one busy pane hold ~150 MB, and the TUI holds an emulator for every
// pane of every open workspace. 3,000 is the owner's choice (2026-11-18).
const paneScrollbackLines = 3000
