package toolchain

import "testing"

func TestNodeArtifact(t *testing.T) {
	cases := []struct {
		name         string
		version      string
		arch         string
		musl         bool
		wantDownload string
		wantSource   string
		wantChecksum string
		wantCacheVer string
		wantPlatform string
	}{
		{
			name:         "glibc x64",
			version:      "22.11.0",
			arch:         "x64",
			musl:         false,
			wantDownload: "node-v22.11.0-linux-x64.tar.gz",
			wantSource:   "https://nodejs.org/dist/v22.11.0/node-v22.11.0-linux-x64.tar.gz",
			wantChecksum: "https://nodejs.org/dist/v22.11.0/SHASUMS256.txt",
			wantCacheVer: "22.11.0",
			wantPlatform: "linux/x64",
		},
		{
			name:         "musl x64 uses unofficial-builds + suffixed cache key",
			version:      "22.11.0",
			arch:         "x64",
			musl:         true,
			wantDownload: "node-v22.11.0-linux-x64-musl.tar.gz",
			wantSource:   "https://unofficial-builds.nodejs.org/download/release/v22.11.0/node-v22.11.0-linux-x64-musl.tar.gz",
			wantChecksum: "https://unofficial-builds.nodejs.org/download/release/v22.11.0/SHASUMS256.txt",
			wantCacheVer: "22.11.0-musl",
			wantPlatform: "linux/x64-musl",
		},
		{
			name:         "musl arm64",
			version:      "22.11.0",
			arch:         "arm64",
			musl:         true,
			wantDownload: "node-v22.11.0-linux-arm64-musl.tar.gz",
			wantSource:   "https://unofficial-builds.nodejs.org/download/release/v22.11.0/node-v22.11.0-linux-arm64-musl.tar.gz",
			wantChecksum: "https://unofficial-builds.nodejs.org/download/release/v22.11.0/SHASUMS256.txt",
			wantCacheVer: "22.11.0-musl",
			wantPlatform: "linux/arm64-musl",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dl, src, chk, cacheVer, platform := nodeArtifact(tc.version, tc.arch, tc.musl)
			if dl != tc.wantDownload {
				t.Errorf("download: got %q, want %q", dl, tc.wantDownload)
			}
			if src != tc.wantSource {
				t.Errorf("source: got %q, want %q", src, tc.wantSource)
			}
			if chk != tc.wantChecksum {
				t.Errorf("checksum: got %q, want %q", chk, tc.wantChecksum)
			}
			if cacheVer != tc.wantCacheVer {
				t.Errorf("cacheVer: got %q, want %q", cacheVer, tc.wantCacheVer)
			}
			if platform != tc.wantPlatform {
				t.Errorf("platform: got %q, want %q", platform, tc.wantPlatform)
			}
		})
	}

	// The libc-variant cache keys MUST differ, or a glibc tree and a musl tree for the
	// same Node version would collide in the cache and the wrong binary get served.
	_, _, _, glibcKey, _ := nodeArtifact("22.11.0", "x64", false)
	_, _, _, muslKey, _ := nodeArtifact("22.11.0", "x64", true)
	if glibcKey == muslKey {
		t.Fatalf("glibc and musl cache keys must differ, both were %q", glibcKey)
	}
}
