package supplychain

// DigestLock tracks non-versioned tag digests over time.
type DigestLock struct {
	Digests map[string]DigestEntry `yaml:"digests"`
}

// DigestEntry records a single image tag's digest.
type DigestEntry struct {
	Digest  string `yaml:"digest"`
	Checked string `yaml:"checked"`
}
