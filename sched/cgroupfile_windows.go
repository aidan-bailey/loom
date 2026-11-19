package sched

// writeControl has no cgroups to write on Windows.
func writeControl(path, data string) error { return ErrUnsupported }
