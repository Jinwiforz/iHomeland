package testclient

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	// FreezeSchemaVersion 是契约冻结记录格式版本。
	FreezeSchemaVersion = 1
	// FreezeAlgorithm 固定路径与内容摘要的组合算法。
	FreezeAlgorithm = "sha256-path-content-v1"
)

// FreezeRecord 是提交到 Git 的 v1 契约聚合摘要。
type FreezeRecord struct {
	// SchemaVersion 选择冻结记录结构代际。
	SchemaVersion int `json:"schemaVersion"`
	// Algorithm 标识可重复实现的聚合摘要算法。
	Algorithm string `json:"algorithm"`
	// Digest 是小写十六进制 SHA-256 聚合值。
	Digest string `json:"digest"`
}

// LoadFreezeRecord 严格解码并验证契约冻结记录。
func LoadFreezeRecord(reader io.Reader) (FreezeRecord, error) {
	if reader == nil {
		return FreezeRecord{}, errors.New("freeze record reader is nil")
	}
	decoder := json.NewDecoder(io.LimitReader(reader, 64<<10))
	decoder.DisallowUnknownFields()
	var record FreezeRecord
	if err := decoder.Decode(&record); err != nil {
		return FreezeRecord{}, fmt.Errorf("decode freeze record: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return FreezeRecord{}, err
	}
	if record.SchemaVersion != FreezeSchemaVersion || record.Algorithm != FreezeAlgorithm {
		return FreezeRecord{}, errors.New("freeze record version or algorithm is unsupported")
	}
	decoded, err := hex.DecodeString(record.Digest)
	if err != nil || len(decoded) != sha256.Size || record.Digest != strings.ToLower(record.Digest) {
		return FreezeRecord{}, errors.New("freeze record digest is not lowercase SHA-256")
	}
	return record, nil
}

// ContractFreezeDigest 计算仓库公开契约源的确定性聚合 SHA-256。
//
// 每项写入 slash-relative path、换行、内容 SHA-256 hex 和换行。路径先按 byte
// 顺序排序；freeze record 自身、generated、descriptor、projection 和运行报告不参与计算。
func ContractFreezeDigest(repositoryRoot string) (string, []string, error) {
	paths, err := contractFreezePaths(repositoryRoot)
	if err != nil {
		return "", nil, err
	}
	aggregate := sha256.New()
	for _, path := range paths {
		content, readErr := fs.ReadFile(osDirFS(repositoryRoot), path)
		if readErr != nil {
			return "", nil, fmt.Errorf("read contract freeze input %q: %w", path, readErr)
		}
		contentDigest := sha256.Sum256(content)
		if _, writeErr := fmt.Fprintf(aggregate, "%s\n%s\n", path, hex.EncodeToString(contentDigest[:])); writeErr != nil {
			return "", nil, fmt.Errorf("hash contract freeze input %q: %w", path, writeErr)
		}
	}
	return hex.EncodeToString(aggregate.Sum(nil)), paths, nil
}

// contractFreezePaths 返回排序后的公开契约源输入清单。
func contractFreezePaths(repositoryRoot string) ([]string, error) {
	patterns := []string{
		"shared/proto/**/*.proto",
		"shared/contracts/http/**/*.yaml",
		"shared/contracts/registry/*.json",
		"shared/contracts/fixtures/**/*.json",
	}
	var paths []string
	for _, pattern := range patterns {
		matches, err := doublestarGlob(repositoryRoot, pattern)
		if err != nil {
			return nil, err
		}
		paths = append(paths, matches...)
	}
	filtered := paths[:0]
	seen := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		path = filepath.ToSlash(filepath.Clean(path))
		if path == "shared/contracts/fixtures/qualification/freeze.json" ||
			strings.HasPrefix(path, "shared/contracts/fixtures/battle/") ||
			strings.HasPrefix(path, "shared/contracts/fixtures/simulation-control/") ||
			strings.Contains(path, "/generated/") ||
			strings.Contains(path, "/descriptor/") ||
			strings.Contains(path, "/projection/") ||
			strings.HasSuffix(path, "/report.json") {
			continue
		}
		if _, exists := seen[path]; exists {
			continue
		}
		seen[path] = struct{}{}
		filtered = append(filtered, path)
	}
	sort.Strings(filtered)
	if len(filtered) == 0 {
		return nil, errors.New("contract freeze input set is empty")
	}
	return filtered, nil
}

// doublestarGlob 展开只包含固定 `/**/` 的仓库相对 pattern。
func doublestarGlob(repositoryRoot, pattern string) ([]string, error) {
	marker := "/**/"
	index := strings.Index(pattern, marker)
	if index < 0 {
		matches, err := filepath.Glob(filepath.Join(repositoryRoot, filepath.FromSlash(pattern)))
		if err != nil {
			return nil, fmt.Errorf("glob contract freeze pattern %q: %w", pattern, err)
		}
		return relativePaths(repositoryRoot, matches)
	}
	base := pattern[:index]
	leaf := pattern[index+len(marker):]
	var matches []string
	err := filepath.WalkDir(filepath.Join(repositoryRoot, filepath.FromSlash(base)), func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		matched, matchErr := filepath.Match(filepath.FromSlash(leaf), entry.Name())
		if matchErr != nil {
			return matchErr
		}
		if matched {
			matches = append(matches, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk contract freeze pattern %q: %w", pattern, err)
	}
	return relativePaths(repositoryRoot, matches)
}

// relativePaths 将绝对或根相对匹配转换为 slash-relative path。
func relativePaths(repositoryRoot string, matches []string) ([]string, error) {
	paths := make([]string, 0, len(matches))
	for _, match := range matches {
		relative, err := filepath.Rel(repositoryRoot, match)
		if err != nil {
			return nil, fmt.Errorf("relativize contract freeze input %q: %w", match, err)
		}
		paths = append(paths, filepath.ToSlash(relative))
	}
	return paths, nil
}

// osDirFS 把仓库根限制为 fs.FS，避免摘要读取逃逸到其他目录。
func osDirFS(root string) fs.FS { return os.DirFS(root) }
