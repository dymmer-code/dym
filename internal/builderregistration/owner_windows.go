package builderregistration

import "os"

func preserveOwner(f *os.File, info os.FileInfo) error { return nil }
