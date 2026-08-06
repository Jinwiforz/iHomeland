// Package gameplaypackage 在任何运行时副作用前冻结 production gameplay package 选择。
//
// 本包只解释部署 identity 与 external binding，不拥有 Ability、AI、damage 或 Unity presentation 语义。
package gameplaypackage

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
)

const (
	// formatVersion 是 production package 当前唯一可部署格式。
	formatVersion = "gameplay-config-format-v1"
	// productionState 隔离 governance fixture 与 runtime consumer。
	productionState = "production"
	// maximumDocumentBytes 限制启动期单份 JSON 的内存占用。
	maximumDocumentBytes = 1 << 20
)

var requiredDocuments = map[string]string{
	"authority.json":    "authority-catalog",
	"bindings.json":     "external-bindings",
	"package.json":      "gameplay-package",
	"presentation.json": "presentation-catalog",
	"wire-mapping.json": "wire-mapping",
}

// Request 保存已经通过纯配置校验的部署选择与跨模块预期摘要。
type Request struct {
	// RootPath 是只供本机 loader 与 child 使用的绝对目录。
	RootPath string
	// ArenaRootPath 是只供本机 arena identity gate 与 child 使用的绝对目录。
	ArenaRootPath string
	// PackageID 是 operator 明确选择的 production package。
	PackageID string
	// ConfigIdentity 是五份 source 的预期聚合摘要。
	ConfigIdentity string
	// NavigationIdentity 是预期 Detour source 摘要。
	NavigationIdentity string
	// PhysicsIdentity 是预期 Jolt source 摘要。
	PhysicsIdentity string
	// WireIdentity 是预期 battle wire manifest 摘要。
	WireIdentity string
	// ModelManifest 是受监督 child 的 model binding。
	ModelManifest string
	// ProfileManifest 是受监督 child 的 network profile binding。
	ProfileManifest string
	// BattleWireIdentity 是公开 BattleTicket/socket 使用的 wire binding。
	BattleWireIdentity string
}

// Selection 是验证后冻结的只读部署结果。
type Selection struct {
	// RootPath 只传给本机 child，不得记录或序列化到协议/evidence。
	RootPath string
	// ArenaRootPath 只传给本机 child，不得记录或写入 control/evidence。
	ArenaRootPath string
	// PackageID 是 production semantic identity。
	PackageID string
	// ConfigIdentity 绑定全部五份 package source。
	ConfigIdentity string
	// NavigationIdentity 绑定 external navigation source。
	NavigationIdentity string
	// PhysicsIdentity 绑定 external physics source。
	PhysicsIdentity string
	// WireIdentity 绑定 battle wire manifest。
	WireIdentity string
	// MappingIdentity 绑定 semantic/numeric mapping source bytes。
	MappingIdentity string
}

type manifestBinding struct {
	Version        string `json:"version"`
	ManifestSHA256 string `json:"manifest_sha256"`
}

type packageDocument struct {
	FormatVersion      string          `json:"format_version"`
	DocumentKind       string          `json:"document_kind"`
	PackageID          string          `json:"package_id"`
	PackageVersion     string          `json:"package_version"`
	QualificationState string          `json:"qualification_state"`
	GovernanceBinding  manifestBinding `json:"governance_binding"`
	ModelBinding       manifestBinding `json:"model_binding"`
	ProfileBinding     manifestBinding `json:"profile_binding"`
	WireBinding        manifestBinding `json:"wire_binding"`
	AuthorityPath      string          `json:"authority_path"`
	PresentationPath   string          `json:"presentation_path"`
	BindingsPath       string          `json:"bindings_path"`
	WireMappingPath    string          `json:"wire_mapping_path"`
	RequiredRoles      []string        `json:"required_roles"`
}

type bindingsDocument struct {
	FormatVersion       string `json:"format_version"`
	DocumentKind        string `json:"document_kind"`
	PackageID           string `json:"package_id"`
	QualificationState  string `json:"qualification_state"`
	MapID               string `json:"map_id"`
	MapContentIdentity  string `json:"map_content_identity"`
	NavigationIdentity  string `json:"navigation_identity"`
	PhysicsIdentity     string `json:"physics_identity"`
	CollisionLayerRef   string `json:"collision_layer_ref"`
	NavigationPolicyRef string `json:"navigation_policy_ref"`
}

type wireMappingDocument struct {
	FormatVersion      string            `json:"format_version"`
	DocumentKind       string            `json:"document_kind"`
	PackageID          string            `json:"package_id"`
	QualificationState string            `json:"qualification_state"`
	Mappings           []json.RawMessage `json:"mappings"`
	RetiredNumericIDs  []uint32          `json:"retired_numeric_ids"`
	RetiredSemanticIDs []string          `json:"retired_semantic_ids"`
}

type documentEnvelope struct {
	FormatVersion      string `json:"format_version"`
	DocumentKind       string `json:"document_kind"`
	PackageID          string `json:"package_id"`
	QualificationState string `json:"qualification_state"`
}

type sourceDocument struct {
	Kind   string
	Digest string
	Bytes  []byte
}

// arenaEnvelope 是 Go selector 对三份 server authority source 所需的最小 closed 投影。
type arenaEnvelope struct {
	FormatVersion string          `json:"format_version"`
	MapID         string          `json:"map_id"`
	Bounds        json.RawMessage `json:"bounds_mm,omitempty"`
	Floor         json.RawMessage `json:"floor,omitempty"`
	StaticBlocks  json.RawMessage `json:"static_blockers,omitempty"`
	SpawnPoints   json.RawMessage `json:"spawn_points,omitempty"`
	Coordinate    string          `json:"coordinate_unit,omitempty"`
	Agent         json.RawMessage `json:"agent,omitempty"`
	Polygons      json.RawMessage `json:"walkable_polygons,omitempty"`
	Links         json.RawMessage `json:"links,omitempty"`
	Gravity       *int64          `json:"gravity_mm_per_second_squared,omitempty"`
	Layers        json.RawMessage `json:"collision_layers,omitempty"`
	QueryPolicy   json.RawMessage `json:"query_policy,omitempty"`
}

// Select 严格验证 closed root、classification 与全部跨边界摘要后返回 immutable snapshot。
func Select(request Request) (Selection, error) {
	if !filepath.IsAbs(request.RootPath) || filepath.Clean(request.RootPath) != request.RootPath {
		return Selection{}, errors.New("gameplay package selection path is invalid")
	}
	if !filepath.IsAbs(request.ArenaRootPath) || filepath.Clean(request.ArenaRootPath) != request.ArenaRootPath {
		return Selection{}, errors.New("gameplay arena selection path is invalid")
	}
	entries, err := os.ReadDir(request.RootPath)
	if err != nil || len(entries) != len(requiredDocuments) {
		return Selection{}, errors.New("gameplay package root is not a closed document set")
	}
	for _, entry := range entries {
		if entry.IsDir() {
			return Selection{}, errors.New("gameplay package root is not a closed document set")
		}
		if _, known := requiredDocuments[entry.Name()]; !known {
			return Selection{}, errors.New("gameplay package root contains an unknown document")
		}
	}

	documents := make(map[string]sourceDocument, len(requiredDocuments))
	for name, kind := range requiredDocuments {
		source, readErr := readDocument(request.RootPath, name)
		if readErr != nil {
			return Selection{}, readErr
		}
		envelope, decodeErr := decodeEnvelope(source)
		if decodeErr != nil || envelope.FormatVersion != formatVersion ||
			envelope.DocumentKind != kind || envelope.PackageID != request.PackageID ||
			envelope.QualificationState != productionState {
			return Selection{}, errors.New("gameplay package document envelope is invalid")
		}
		documents[name] = sourceDocument{Kind: kind, Digest: sha256Hex(source), Bytes: source}
	}

	var manifest packageDocument
	if err := decodeClosed(documents["package.json"].Bytes, &manifest); err != nil ||
		!validPackageDocument(manifest, request) {
		return Selection{}, errors.New("gameplay package manifest binding is invalid")
	}
	var bindings bindingsDocument
	if err := decodeClosed(documents["bindings.json"].Bytes, &bindings); err != nil ||
		!validBindingsDocument(bindings, request) {
		return Selection{}, errors.New("gameplay package external binding is invalid")
	}
	var mapping wireMappingDocument
	if err := decodeClosed(documents["wire-mapping.json"].Bytes, &mapping); err != nil ||
		mapping.FormatVersion != formatVersion || mapping.DocumentKind != "wire-mapping" ||
		mapping.PackageID != request.PackageID || mapping.QualificationState != productionState ||
		len(mapping.Mappings) == 0 {
		return Selection{}, errors.New("gameplay package wire mapping binding is invalid")
	}

	configIdentity := productionIdentity(manifest.GovernanceBinding.ManifestSHA256, documents)
	if configIdentity != request.ConfigIdentity ||
		bindings.NavigationIdentity != request.NavigationIdentity ||
		bindings.PhysicsIdentity != request.PhysicsIdentity {
		return Selection{}, errors.New("gameplay package source identity differs from deployment selection")
	}
	if err := validateArenaSources(request.ArenaRootPath, bindings); err != nil {
		return Selection{}, err
	}
	return Selection{
		RootPath:           request.RootPath,
		ArenaRootPath:      request.ArenaRootPath,
		PackageID:          request.PackageID,
		ConfigIdentity:     configIdentity,
		NavigationIdentity: bindings.NavigationIdentity,
		PhysicsIdentity:    bindings.PhysicsIdentity,
		WireIdentity:       manifest.WireBinding.ManifestSHA256,
		MappingIdentity:    documents["wire-mapping.json"].Digest,
	}, nil
}

func validPackageDocument(document packageDocument, request Request) bool {
	return document.FormatVersion == formatVersion && document.DocumentKind == "gameplay-package" &&
		document.PackageID == request.PackageID && document.PackageVersion == request.PackageID &&
		document.QualificationState == productionState &&
		document.AuthorityPath == "authority.json" && document.PresentationPath == "presentation.json" &&
		document.BindingsPath == "bindings.json" && document.WireMappingPath == "wire-mapping.json" &&
		document.GovernanceBinding.Version == formatVersion && validDigest(document.GovernanceBinding.ManifestSHA256) &&
		document.ModelBinding.Version == "battle-model-v1" && document.ModelBinding.ManifestSHA256 == request.ModelManifest &&
		document.ProfileBinding.Version == "battle-network-profile-v2" && document.ProfileBinding.ManifestSHA256 == request.ProfileManifest &&
		document.WireBinding.Version == "battle-wire-v1" && document.WireBinding.ManifestSHA256 == request.WireIdentity &&
		document.WireBinding.ManifestSHA256 == request.BattleWireIdentity && sortedUniqueNonempty(document.RequiredRoles)
}

func validBindingsDocument(document bindingsDocument, request Request) bool {
	return document.FormatVersion == formatVersion && document.DocumentKind == "external-bindings" &&
		document.PackageID == request.PackageID && document.QualificationState == productionState &&
		document.MapID != "" && validDigest(document.MapContentIdentity) &&
		document.NavigationIdentity == request.NavigationIdentity && document.PhysicsIdentity == request.PhysicsIdentity &&
		document.CollisionLayerRef != "" && document.NavigationPolicyRef != "" &&
		document.MapContentIdentity != document.NavigationIdentity &&
		document.MapContentIdentity != document.PhysicsIdentity &&
		document.NavigationIdentity != document.PhysicsIdentity
}

// validateArenaSources 在任何 child/listener 副作用前冻结三份 server authority source。
func validateArenaSources(root string, bindings bindingsDocument) error {
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 3 {
		return errors.New("gameplay arena root is not a closed document set")
	}
	expected := map[string]string{
		"arena.json":      bindings.MapContentIdentity,
		"navigation.json": bindings.NavigationIdentity,
		"physics.json":    bindings.PhysicsIdentity,
	}
	for _, entry := range entries {
		if entry.IsDir() {
			return errors.New("gameplay arena root is not a closed document set")
		}
		if _, ok := expected[entry.Name()]; !ok {
			return errors.New("gameplay arena root contains an unknown document")
		}
	}
	versions := map[string]string{
		"arena.json":      "personal-world-arena-v1",
		"navigation.json": "personal-world-navigation-v1",
		"physics.json":    "personal-world-physics-v1",
	}
	for name, digest := range expected {
		source, readErr := readDocument(root, name)
		if readErr != nil || sha256Hex(source) != digest {
			return errors.New("gameplay arena source identity differs")
		}
		var envelope arenaEnvelope
		if decodeErr := decodeClosed(source, &envelope); decodeErr != nil ||
			envelope.FormatVersion != versions[name] || envelope.MapID != bindings.MapID {
			return errors.New("gameplay arena document binding is invalid")
		}
	}
	return nil
}

func readDocument(root string, name string) ([]byte, error) {
	path := filepath.Join(root, name)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maximumDocumentBytes {
		return nil, errors.New("gameplay package document source is invalid")
	}
	source, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("gameplay package document cannot be read")
	}
	if err := rejectDuplicateNames(source); err != nil {
		return nil, errors.New("gameplay package document JSON is invalid")
	}
	return source, nil
}

func decodeEnvelope(source []byte) (documentEnvelope, error) {
	var envelope documentEnvelope
	err := json.Unmarshal(source, &envelope)
	return envelope, err
}

func decodeClosed(source []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(source))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("trailing JSON value")
	}
	return nil
}

func rejectDuplicateNames(source []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(source))
	if err := scanJSONValue(decoder); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return errors.New("trailing JSON token")
	}
	return nil
}

func scanJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, compound := token.(json.Delim)
	if !compound {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]struct{}{}
		for decoder.More() {
			keyToken, keyErr := decoder.Token()
			key, ok := keyToken.(string)
			if keyErr != nil || !ok {
				return errors.New("invalid JSON object key")
			}
			if _, duplicate := seen[key]; duplicate {
				return errors.New("duplicate JSON object key")
			}
			seen[key] = struct{}{}
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
	default:
		return errors.New("invalid JSON delimiter")
	}
	closing, err := decoder.Token()
	if err != nil || closing != json.Delim(map[json.Delim]json.Delim{'{': '}', '[': ']'}[delimiter]) {
		return errors.New("invalid JSON closing delimiter")
	}
	return nil
}

func productionIdentity(governance string, documents map[string]sourceDocument) string {
	paths := make([]string, 0, len(documents))
	for path := range documents {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	buffer := bytes.NewBufferString("ihomeland-gameplay-production-v1\n" + governance + "\n")
	for _, path := range paths {
		document := documents[path]
		buffer.WriteString(path)
		buffer.WriteByte(0)
		buffer.WriteString(document.Kind)
		buffer.WriteByte(0)
		buffer.WriteString(document.Digest)
		buffer.WriteByte('\n')
	}
	return sha256Hex(buffer.Bytes())
}

func sha256Hex(source []byte) string {
	digest := sha256.Sum256(source)
	return hex.EncodeToString(digest[:])
}

func validDigest(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func sortedUniqueNonempty(values []string) bool {
	if len(values) == 0 {
		return false
	}
	for index, value := range values {
		if value == "" || (index > 0 && values[index-1] >= value) {
			return false
		}
	}
	return true
}
