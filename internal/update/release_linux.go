package update

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const (
	repository     = "iamcheyan/bedrockchat"
	latestURL      = "https://api.github.com/repos/" + repository + "/releases/latest"
	downloadPrefix = "https://github.com/" + repository + "/releases/download/"
	checksumsName  = "SHA256SUMS"
	maxReleaseSize = 4 << 20
	maxSumsSize    = 64 << 10
	minBinarySize  = 4096
	maxBinarySize  = 128 << 20
)

type release struct {
	Tag        string  `json:"tag_name"`
	Draft      bool    `json:"draft"`
	Prerelease bool    `json:"prerelease"`
	Assets     []asset `json:"assets"`
}

type asset struct {
	Name   string `json:"name"`
	URL    string `json:"browser_download_url"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"`
}

var semver = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$`)

func parseVersion(s string) ([]string, error) {
	m := semver.FindStringSubmatch(s)
	if m == nil {
		return nil, fmt.Errorf("invalid semantic version %q", s)
	}
	for id := range strings.SplitSeq(m[4], ".") {
		if len(id) > 1 && id[0] == '0' && strings.Trim(id, "0123456789") == "" {
			return nil, fmt.Errorf("invalid semantic version %q", s)
		}
	}
	return m, nil
}

// The candidate is always stable, so only the core and the presence of a
// prerelease on current affect precedence. Compare numbers without overflow.
func isNewer(stable, current []string) bool {
	if current == nil { // Development build.
		return true
	}
	for i := 1; i <= 3; i++ {
		if len(stable[i]) != len(current[i]) {
			return len(stable[i]) > len(current[i])
		}
		if stable[i] != current[i] {
			return stable[i] > current[i]
		}
	}
	return current[4] != ""
}

func newClient() *http.Client {
	return &http.Client{
		Timeout: 2 * time.Minute,
		Transport: &http.Transport{
			DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			ForceAttemptHTTP2:     true,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 20 * time.Second,
			IdleConnTimeout:       30 * time.Second,
			DisableCompression:    true,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			u := req.URL
			if len(via) >= 5 || via[0].URL.Host != "github.com" || u.Scheme != "https" || u.User != nil || u.Opaque != "" || u.Port() != "" {
				return errors.New("unexpected GitHub download redirect")
			}
			switch u.Host {
			case "release-assets.githubusercontent.com", "objects.githubusercontent.com", "github-releases.githubusercontent.com":
			default:
				return errors.New("unexpected GitHub download redirect host")
			}
			// Do not propagate any headers, including Referer (signed URLs),
			// credentials or cookies. This client has no cookie jar.
			req.Header = make(http.Header)
			return nil
		},
	}
}

func get(ctx context.Context, client *http.Client, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if url == latestURL {
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("GitHub returned HTTP %d", resp.StatusCode)
	}
	return resp, nil
}

func latest(ctx context.Context, client *http.Client) (release, error) {
	var rel release
	resp, err := get(ctx, client, latestURL)
	if err != nil {
		return rel, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxReleaseSize+1))
	if err != nil {
		return rel, err
	}
	if len(data) > maxReleaseSize {
		return rel, errors.New("release metadata exceeds size limit")
	}
	if err := json.Unmarshal(data, &rel); err != nil {
		return rel, err
	}
	return rel, nil
}

func (r release) findAsset(name string, min, max int64) (asset, error) {
	var match *asset
	for i := range r.Assets {
		a := &r.Assets[i]
		if a.Name == name {
			if match != nil {
				return asset{}, fmt.Errorf("duplicate release asset %s", name)
			}
			match = a
		}
	}
	if match == nil {
		return asset{}, fmt.Errorf("release %s has no asset %s", r.Tag, name)
	}
	if match.URL != downloadPrefix+r.Tag+"/"+name || match.Size < min || match.Size > max {
		return asset{}, fmt.Errorf("unexpected URL or size for release asset %s", name)
	}
	if match.Digest != "" {
		if sum, ok := strings.CutPrefix(match.Digest, "sha256:"); !ok || !validSHA256(sum) {
			return asset{}, fmt.Errorf("invalid GitHub SHA-256 digest for %s", name)
		}
	}
	return *match, nil
}

func validSHA256(s string) bool {
	if len(s) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

func (a asset) download(ctx context.Context, client *http.Client, dst io.Writer) (string, error) {
	resp, err := get(ctx, client, a.URL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.ContentLength >= 0 && resp.ContentLength != a.Size {
		return "", errors.New("Content-Length differs from release asset size")
	}
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(dst, hash), io.LimitReader(resp.Body, a.Size+1))
	if err != nil {
		return "", err
	}
	if n != a.Size {
		return "", fmt.Errorf("expected %d bytes, downloaded %d", a.Size, n)
	}
	sum := hex.EncodeToString(hash.Sum(nil))
	if a.Digest != "" && !strings.EqualFold(a.Digest, "sha256:"+sum) {
		return "", errors.New("GitHub SHA-256 digest mismatch")
	}
	return sum, nil
}

func publishedChecksum(data *bytes.Buffer, name string) (string, error) {
	var checksum string
	for line := range strings.SplitSeq(data.String(), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) != 2 || !validSHA256(fields[0]) {
			return "", errors.New("malformed SHA256SUMS entry")
		}
		if strings.TrimPrefix(fields[1], "*") == name {
			if checksum != "" {
				return "", fmt.Errorf("duplicate SHA256SUMS entry for %s", name)
			}
			checksum = strings.ToLower(fields[0])
		}
	}
	if checksum == "" {
		return "", fmt.Errorf("SHA256SUMS has no entry for %s", name)
	}
	return checksum, nil
}
