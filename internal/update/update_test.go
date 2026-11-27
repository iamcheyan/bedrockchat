//go:build linux

package update

import (
	"bytes"
	"context"
	"crypto/sha256"
	"debug/elf"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The updater must never execute a download. This ELF fixture has the structural
// fields the Linux loader needs, but its payload is deliberately not runnable.
func fixtureELF(machine elf.Machine) []byte {
	b := make([]byte, 4096)
	copy(b, "\x7fELF\x02\x01\x01")
	bo := binary.LittleEndian
	bo.PutUint16(b[16:], uint16(elf.ET_EXEC))
	bo.PutUint16(b[18:], uint16(machine))
	bo.PutUint32(b[20:], 1)
	bo.PutUint64(b[24:], 0x400078) // entry address
	bo.PutUint64(b[32:], 64)       // program header offset
	bo.PutUint16(b[52:], 64)
	bo.PutUint16(b[54:], 56)
	bo.PutUint16(b[56:], 1)
	bo.PutUint32(b[64:], uint32(elf.PT_LOAD))
	bo.PutUint32(b[68:], uint32(elf.PF_R|elf.PF_X))
	bo.PutUint64(b[80:], 0x400000)
	bo.PutUint64(b[96:], uint64(len(b)))
	bo.PutUint64(b[104:], uint64(len(b)))
	bo.PutUint64(b[112:], 4096)
	return b
}

const fixtureReleaseURL = "https://api.github.com/repos/iamcheyan/bedrockchat/releases/latest"

func fixtureAssetURL(tag, name string) string {
	return "https://github.com/iamcheyan/bedrockchat/releases/download/" + tag + "/" + name
}

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestUpdate(t *testing.T) {
	machine := map[string]elf.Machine{"amd64": elf.EM_X86_64, "arm64": elf.EM_AARCH64}[runtime.GOARCH]
	if machine == elf.EM_NONE {
		t.Skip("updater supports amd64 and arm64")
	}
	name := "bedrockchat-linux-" + runtime.GOARCH
	for _, tc := range []struct {
		name, current, tag    string
		draft, prerelease     bool
		change                func(*asset, *asset, *[]byte, *[]byte)
		targetChange          func(string) error
		wantError, wantUpdate bool
		wantRequests          int
	}{
		{name: "updates a development build", current: "dev", tag: "v1.2.3", wantUpdate: true, wantRequests: 3},
		{name: "updates a prerelease of same core", current: "v1.2.3-rc.1", tag: "v1.2.3", wantUpdate: true, wantRequests: 3},
		{name: "compares components numerically", current: "v1.9.9", tag: "v1.10.0", wantUpdate: true, wantRequests: 3},
		{name: "checksums work for legacy assets", current: "v1.2.2", tag: "v1.2.3", wantUpdate: true, wantRequests: 3, change: func(bin, sums *asset, _, _ *[]byte) { bin.Digest, sums.Digest = "", "" }},
		{name: "does not downgrade", current: "v2.0.0", tag: "v1.20.0", wantRequests: 1},
		{name: "ignores build metadata for precedence", current: "v1.2.3+local.7", tag: "v1.2.3", wantRequests: 1},
		{name: "rejects unknown current versions", current: "typo", tag: "v1.2.3", wantError: true},
		{name: "rejects leading-zero tag", current: "dev", tag: "v01.2.3", wantError: true, wantRequests: 1},
		{name: "rejects prerelease tag", current: "dev", tag: "v1.2.3-rc.1", wantError: true, wantRequests: 1},
		{name: "rejects prerelease flag", current: "dev", tag: "v1.2.3", prerelease: true, wantError: true, wantRequests: 1},
		{name: "rejects draft", current: "dev", tag: "v1.2.3", draft: true, wantError: true, wantRequests: 1},
		{name: "rejects lacking asset", current: "dev", tag: "v1.2.3", wantError: true, wantRequests: 1, change: func(bin, _ *asset, _, _ *[]byte) { bin.Name = "other" }},
		{name: "rejects duplicate asset", current: "dev", tag: "v1.2.3", wantError: true, wantRequests: 1, change: func(bin, sums *asset, _, _ *[]byte) { *sums = *bin }},
		{name: "rejects asset from other repository", current: "dev", tag: "v1.2.3", wantError: true, wantRequests: 1, change: func(bin, _ *asset, _, _ *[]byte) { bin.URL = strings.Replace(bin.URL, "/iamcheyan/", "/other/", 1) }},
		{name: "rejects bad checksum", current: "dev", tag: "v1.2.3", wantError: true, wantRequests: 2, change: func(bin, sums *asset, _, data *[]byte) {
			*data = []byte(fmt.Sprintf("%064d  %s\n", 0, bin.Name))
			sums.Digest = fmt.Sprintf("sha256:%x", sha256.Sum256(*data))
		}},
		{name: "rejects missing checksum entry", current: "dev", tag: "v1.2.3", wantError: true, wantRequests: 2, change: func(_, sums *asset, _, data *[]byte) {
			*data = bytes.ReplaceAll(*data, []byte(name), []byte(strings.Repeat("x", len(name))))
			sums.Digest = fmt.Sprintf("sha256:%x", sha256.Sum256(*data))
		}},
		{name: "rejects duplicate checksum entry", current: "dev", tag: "v1.2.3", wantError: true, wantRequests: 2, change: func(_, sums *asset, _, data *[]byte) {
			*data = append(*data, *data...)
			sums.Size = int64(len(*data))
			sums.Digest = fmt.Sprintf("sha256:%x", sha256.Sum256(*data))
		}},
		{name: "rejects bad GitHub checksums digest", current: "dev", tag: "v1.2.3", wantError: true, wantRequests: 2, change: func(_, sums *asset, _, _ *[]byte) { sums.Digest = fmt.Sprintf("sha256:%064d", 0) }},
		{name: "rejects malformed GitHub binary digest", current: "dev", tag: "v1.2.3", wantError: true, wantRequests: 1, change: func(bin, _ *asset, _, _ *[]byte) { bin.Digest = "sha256:bogus" }},
		{name: "rejects an oversized declared asset", current: "dev", tag: "v1.2.3", wantError: true, wantRequests: 1, change: func(bin, _ *asset, _, _ *[]byte) { bin.Size = 129 << 20 }},
		{name: "rejects an undersized declared asset", current: "dev", tag: "v1.2.3", wantError: true, wantRequests: 1, change: func(bin, _ *asset, _, _ *[]byte) { bin.Size = 100 }},
		{name: "rejects an oversized streamed body", current: "dev", tag: "v1.2.3", wantError: true, wantRequests: 3, change: func(_, _ *asset, binary, _ *[]byte) { *binary = append(*binary, 1) }},
		{name: "rejects a truncated body", current: "dev", tag: "v1.2.3", wantError: true, wantRequests: 3, change: func(_, _ *asset, binary, _ *[]byte) { *binary = (*binary)[:100] }},
		{name: "rejects a tampered binary", current: "dev", tag: "v1.2.3", wantError: true, wantRequests: 3, change: func(_, _ *asset, binary, _ *[]byte) { (*binary)[1000]++ }},
		{name: "rejects checksum mismatch without GitHub digest", current: "dev", tag: "v1.2.3", wantError: true, wantRequests: 3, change: func(bin, _ *asset, binary, _ *[]byte) {
			bin.Digest = ""
			(*binary)[1000]++
		}},
		{name: "rejects the wrong ELF machine", current: "dev", tag: "v1.2.3", wantError: true, wantRequests: 3, change: func(bin, sums *asset, data, list *[]byte) {
			other := elf.EM_AARCH64
			if machine == other {
				other = elf.EM_X86_64
			}
			*data = fixtureELF(other)
			setFixtureDigests(bin, sums, *data, list)
		}},
		{name: "rejects plain text even with valid digests", current: "dev", tag: "v1.2.3", wantError: true, wantRequests: 3, change: func(bin, sums *asset, data, list *[]byte) {
			*data = bytes.Repeat([]byte("x"), len(*data))
			setFixtureDigests(bin, sums, *data, list)
		}},
		{name: "rejects a symlink at the install path", current: "dev", tag: "v1.2.3", wantError: true, wantRequests: 2, targetChange: func(path string) error {
			if err := os.Rename(path, path+".old"); err != nil {
				return err
			}
			return os.Symlink(path+".old", path)
		}},
		{name: "rejects a different inode at the install path", current: "dev", tag: "v1.2.3", wantError: true, wantRequests: 2, targetChange: func(path string) error {
			if err := os.Rename(path, path+".old"); err != nil {
				return err
			}
			return os.WriteFile(path, []byte("old"), 0750)
		}},
		{name: "rejects setuid executable", current: "dev", tag: "v1.2.3", wantError: true, wantRequests: 2, targetChange: func(path string) error { return os.Chmod(path, 0750|os.ModeSetuid) }},
		{name: "rejects group-writable executable", current: "dev", tag: "v1.2.3", wantError: true, wantRequests: 2, targetChange: func(path string) error { return os.Chmod(path, 0775) }},
		{name: "rejects group-writable executable directory", current: "dev", tag: "v1.2.3", wantError: true, wantRequests: 2, targetChange: func(path string) error { return os.Chmod(filepath.Dir(path), 0770) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.Chmod(dir, 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "bedrockchat")
			if err := os.WriteFile(path, []byte("old"), 0750); err != nil {
				t.Fatal(err)
			}
			running, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if tc.targetChange != nil {
				if err := tc.targetChange(path); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.Lstat(path)
			if err != nil {
				t.Fatal(err)
			}
			entriesBefore, err := os.ReadDir(filepath.Dir(path))
			if err != nil {
				t.Fatal(err)
			}
			binaryAsset := asset{Name: name, URL: fixtureAssetURL(tc.tag, name)}
			sumsAsset := asset{Name: "SHA256SUMS", URL: fixtureAssetURL(tc.tag, "SHA256SUMS")}
			data := fixtureELF(machine)
			var sums []byte
			setFixtureDigests(&binaryAsset, &sumsAsset, data, &sums)
			if tc.change != nil {
				tc.change(&binaryAsset, &sumsAsset, &data, &sums)
			}
			metadata, err := json.Marshal(release{Tag: tc.tag, Draft: tc.draft, Prerelease: tc.prerelease, Assets: []asset{binaryAsset, sumsAsset}})
			if err != nil {
				t.Fatal(err)
			}
			requests := 0
			client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				requests++
				var body []byte
				switch r.URL.String() {
				case fixtureReleaseURL:
					body = metadata
				case fixtureAssetURL(tc.tag, "SHA256SUMS"):
					body = sums
				case fixtureAssetURL(tc.tag, name):
					body = data
				default:
					t.Fatalf("unexpected request %s", r.URL)
				}
				return &http.Response{StatusCode: 200, ContentLength: -1, Body: io.NopCloser(bytes.NewReader(body)), Header: make(http.Header)}, nil
			})}
			var out bytes.Buffer
			err = run(context.Background(), tc.current, &out, client, func() (string, os.FileInfo, error) { return path, running, nil })
			if (err != nil) != tc.wantError {
				t.Errorf("run returned %v, want error: %v", err, tc.wantError)
			}
			if requests != tc.wantRequests {
				t.Errorf("requests = %d, want %d", requests, tc.wantRequests)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			info, err := os.Lstat(path)
			if err != nil {
				t.Fatal(err)
			}
			if tc.wantUpdate {
				if !bytes.Equal(got, data) || info.Mode() != 0750 || os.SameFile(before, info) || !strings.Contains(out.String(), tc.tag) {
					t.Errorf("not replaced as expected; mode %v, output %q", info.Mode(), &out)
				}
			} else if string(got) != "old" || info.Mode() != before.Mode() || !os.SameFile(before, info) {
				t.Errorf("target changed on error/no-op")
			}
			entriesAfter, err := os.ReadDir(filepath.Dir(path))
			if err != nil || len(entriesAfter) != len(entriesBefore) {
				t.Errorf("left staging files: %v (%v)", entriesAfter, err)
			}
		})
	}
}

func setFixtureDigests(bin, sums *asset, data []byte, list *[]byte) {
	bin.Size = int64(len(data))
	digest := sha256.Sum256(data)
	bin.Digest = fmt.Sprintf("sha256:%x", digest)
	*list = fmt.Appendf(nil, "%x  %s\n", digest, bin.Name)
	sums.Size = int64(len(*list))
	sums.Digest = fmt.Sprintf("sha256:%x", sha256.Sum256(*list))
}

func TestReleaseRedirects(t *testing.T) {
	for _, tc := range []struct {
		destination, initial string
		allowed              bool
	}{
		{"https://release-assets.githubusercontent.com/path?signature=example", "github.com", true},
		{"https://objects.githubusercontent.com/path", "github.com", true},
		{"https://objects.githubusercontent.com/path", "api.github.com", false},
		{"http://release-assets.githubusercontent.com/path", "github.com", false},
		{"https://release-assets.githubusercontent.com.evil.test/path", "github.com", false},
		{"https://" + "user@" + "release-assets.githubusercontent.com/path", "github.com", false},
		{"https://release-assets.githubusercontent.com:8443/path", "github.com", false},
		{"https://github.com/other/repo/releases/download/v1.2.3/file", "github.com", false},
	} {
		t.Run(tc.destination+" from "+tc.initial, func(t *testing.T) {
			client := newClient()
			requests := 0
			client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
				requests++
				header := make(http.Header)
				status := http.StatusOK
				if requests == 1 {
					status = http.StatusFound
					header.Set("Location", tc.destination)
				} else {
					for _, h := range []string{"Authorization", "Cookie", "Referer", "X-Custom-Token"} {
						if r.Header.Get(h) != "" {
							t.Errorf("forwarded %s", h)
						}
					}
				}
				return &http.Response{StatusCode: status, Header: header, Body: http.NoBody}, nil
			})
			req := &http.Request{Method: http.MethodGet, URL: &url.URL{Scheme: "https", Host: tc.initial, Path: "/example"}, Header: make(http.Header)}
			for _, h := range []string{"Authorization", "Cookie", "Referer", "X-Custom-Token"} {
				req.Header.Set(h, "do not forward")
			}
			res, err := client.Do(req)
			if err == nil {
				res.Body.Close()
			}
			if (err == nil) != tc.allowed || (requests == 2) != tc.allowed {
				t.Errorf("allowed = %v, requests = %d, error = %v", tc.allowed, requests, err)
			}
		})
	}
}
