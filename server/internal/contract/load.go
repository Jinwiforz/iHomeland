package contract

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Load 从仓库根目录读取全部 registry，并在业务校验前尽早拒绝格式错误的 JSON。
//
// root 必须指向包含 shared/contracts 的仓库根目录。任何文件缺失或解码失败都会使整个加载
// 失败，不返回部分 Catalog，避免后续校验误把默认零值当作真实治理配置。
func Load(root string) (Catalog, error) {
	// 只有各 owner 的 registry 全部解码成功后才返回 catalog，避免调用方拿到部分有效状态。
	var catalog Catalog
	registryRoot := filepath.Join(root, "shared", "contracts", "registry")
	if err := readJSON(filepath.Join(registryRoot, "messages.json"), &catalog.Messages); err != nil {
		return Catalog{}, err
	}
	if err := readJSON(filepath.Join(registryRoot, "errors.json"), &catalog.Errors); err != nil {
		return Catalog{}, err
	}
	if err := readJSON(filepath.Join(registryRoot, "routes.json"), &catalog.Routes); err != nil {
		return Catalog{}, err
	}
	return catalog, nil
}

// readJSON 使用严格解码，防止拼写错误的治理字段被静默忽略。
//
// destination 必须是目标 registry 结构的可写指针。错误保留具体路径和底层原因，便于 CI
// 精确指出损坏的契约源；该函数不负责业务语义校验。
func readJSON(path string, destination any) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	// 只读文件的关闭错误不会改变已经解码的内存快照，也没有待刷新的写入数据。
	defer func() { _ = file.Close() }()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	// 第二次解码必须直接到达 EOF，防止合法 registry 后附加的第二个 JSON 值逃过校验。
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("decode %s: trailing JSON value", path)
		}
		return fmt.Errorf("decode %s trailing content: %w", path, err)
	}
	return nil
}
