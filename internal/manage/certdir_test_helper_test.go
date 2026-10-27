package manage

import "os"

// configDirWritable reports whether buildSpec can generate a self-signed
// certificate, which needs a writable config directory.
func configDirWritable() bool {
	f, err := os.CreateTemp(appConfigDir, ".certcheck-*")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	return true
}
