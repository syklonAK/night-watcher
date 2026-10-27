package edge

import "os"

func reloadSignal() (chan os.Signal, func()) { return nil, func() {} }
