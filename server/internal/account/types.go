package account

import (
	"errors"
	"log/slog"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

const (
	// minimumUsernameBytes 与公开 OpenAPI 下界一致；username 只接受 ASCII，因此 byte 与 character 等价。
	minimumUsernameBytes = 3
	// maximumUsernameBytes 限制唯一索引、请求成本和跨端表示，必须与 OpenAPI pattern 同步修改。
	maximumUsernameBytes = 64
	// maximumDisplayNameRunes 在 NFC 与空白折叠后计数，避免组合形式绕过公开展示上限。
	maximumDisplayNameRunes = 32
	// maximumIdentifierBytes 约束 repository key 和安全关联值，防止异常 generator 放大存储与日志。
	maximumIdentifierBytes = 128
	// accountIDPrefix 把共享随机材料固定到 account namespace，防止不同实体 ID 被误用。
	accountIDPrefix = "acc_"
	// playerIDPrefix 把共享随机材料固定到 player namespace，避免账号身份直接充当业务玩家身份。
	playerIDPrefix = "ply_"
	// credentialPlaceholder 是 password/hash 所有默认格式化路径唯一允许输出的稳定文本。
	credentialPlaceholder = "[REDACTED_CREDENTIAL]"
)

// AccountID 标识不可由客户端选择的持久账号事实。
type AccountID struct {
	// value 保存带 account namespace 前缀的安全 ASCII identifier。
	value string
}

// NewAccountID 校验 repository hydration 或服务端生成的账号 identifier。
func NewAccountID(value string) (AccountID, error) {
	if err := validateIdentifier(value, accountIDPrefix); err != nil {
		return AccountID{}, err
	}
	return AccountID{value: value}, nil
}

// String 返回可用于持久索引的非凭据 identifier。
func (id AccountID) String() string { return id.value }

// Valid 报告 AccountID 是否经过完整构造。
func (id AccountID) Valid() bool { return id.value != "" }

// PlayerID 标识账号拥有的玩家业务身份。
type PlayerID struct {
	// value 保存带 player namespace 前缀的安全 ASCII identifier。
	value string
}

// NewPlayerID 校验 repository hydration 或服务端生成的玩家 identifier。
func NewPlayerID(value string) (PlayerID, error) {
	if err := validateIdentifier(value, playerIDPrefix); err != nil {
		return PlayerID{}, err
	}
	return PlayerID{value: value}, nil
}

// String 返回可用于业务授权和房间成员索引的非凭据 identifier。
func (id PlayerID) String() string { return id.value }

// Valid 报告 PlayerID 是否经过完整构造。
func (id PlayerID) Valid() bool { return id.value != "" }

// Username 是登录与唯一约束使用的 ASCII lowercase canonical key。
type Username struct {
	// canonical 只保存规范化结果，避免 repository 再次解释大小写规则。
	canonical string
}

// NewUsername 校验受限 ASCII username 并生成唯一 canonical form。
//
// 输入不做 trim；任何空白或 Unicode 字符直接失败，防止不同客户端、数据库 collation
// 或 normalization library 对同一登录 key 产生不同解释。错误不包含原始 username。
func NewUsername(value string) (Username, error) {
	if len(value) < minimumUsernameBytes || len(value) > maximumUsernameBytes {
		return Username{}, errors.New("username length is invalid")
	}
	for index := 0; index < len(value); index++ {
		if !usernameCharacterAllowed(value[index]) {
			return Username{}, errors.New("username contains unsupported characters")
		}
	}
	if !asciiAlphaNumeric(value[0]) || !asciiAlphaNumeric(value[len(value)-1]) {
		return Username{}, errors.New("username boundary is invalid")
	}
	return Username{canonical: asciiLower(value)}, nil
}

// String 返回 repository 唯一索引使用的 canonical username。
func (username Username) String() string { return username.canonical }

// Valid 报告 Username 是否经过完整构造。
func (username Username) Valid() bool { return username.canonical != "" }

// DisplayName 是经过 Unicode 安全规范化的公开展示文本。
type DisplayName struct {
	// value 保存 NFC 且空白折叠后的稳定投影。
	value string
}

// NewDisplayName 执行 UTF-8、NFC、空白和危险控制字符边界。
//
// NFC 先于计数，连续 Unicode space separator 折叠为单个 ASCII space；control 与 format
// 字符即使具有视觉效果也被拒绝，避免日志、UI 和审核工具显示不同文本。结果只用于展示，
// 不能作为登录、唯一索引或授权依据。
func NewDisplayName(value string) (DisplayName, error) {
	if !utf8.ValidString(value) {
		return DisplayName{}, errors.New("display name is not valid UTF-8")
	}
	normalized := norm.NFC.String(value)
	var builder strings.Builder
	pendingSpace := false
	count := 0
	for _, character := range normalized {
		if unicode.IsControl(character) || unicode.In(character, unicode.Cf) {
			return DisplayName{}, errors.New("display name contains unsafe characters")
		}
		if unicode.IsSpace(character) {
			if count > 0 {
				pendingSpace = true
			}
			continue
		}
		if pendingSpace {
			builder.WriteByte(' ')
			count++
			pendingSpace = false
		}
		builder.WriteRune(character)
		count++
		if count > maximumDisplayNameRunes {
			return DisplayName{}, errors.New("display name is too long")
		}
	}
	if count == 0 {
		return DisplayName{}, errors.New("display name is empty")
	}
	return DisplayName{value: builder.String()}, nil
}

// String 返回可公开展示的规范化文本。
func (name DisplayName) String() string { return name.value }

// Valid 报告 DisplayName 是否经过完整构造。
func (name DisplayName) Valid() bool { return name.value != "" }

// Status 表达账号是否允许通过凭据建立新 session。
type Status uint8

const (
	// StatusUnspecified 是禁止进入 repository 的零值。
	StatusUnspecified Status = iota
	// StatusActive 允许凭据匹配后建立独立 session。
	StatusActive
	// StatusInactive 合并停用类状态，避免 login 暴露具体管理原因。
	StatusInactive
)

// Valid 报告 status 是否属于 repository 可以返回的封闭集合。
func (status Status) Valid() bool { return status == StatusActive || status == StatusInactive }

// Account 保存注册事务必须原子提交的账号和玩家身份事实。
type Account struct {
	// id 是持久账号主身份。
	id AccountID
	// playerID 是 session principal 和业务授权使用的玩家身份。
	playerID PlayerID
	// username 是唯一登录 key，不进入公开 summary。
	username Username
	// displayName 是规范化后的公开展示值。
	displayName DisplayName
	// status 决定账号能否建立新 session。
	status Status
	// createdAt 是服务端选择且持久化的绝对创建时间。
	createdAt time.Time
}

// NewAccount 校验 repository 与 application 共享的完整账号不变量。
//
// createdAt 在构造时转换为 UTC 并移除单调时钟分量，使持久化和跨进程比较不依赖本机
// wall clock 表示；ID、status 或时间不能来自客户端 payload。
func NewAccount(id AccountID, playerID PlayerID, username Username, displayName DisplayName, status Status, createdAt time.Time) (Account, error) {
	if !id.Valid() || !playerID.Valid() || !username.Valid() || !displayName.Valid() || !status.Valid() || createdAt.IsZero() {
		return Account{}, errors.New("account facts are incomplete")
	}
	return Account{id: id, playerID: playerID, username: username, displayName: displayName, status: status, createdAt: createdAt.UTC()}, nil
}

// ID 返回不可变账号身份。
func (account Account) ID() AccountID { return account.id }

// PlayerID 返回不可变玩家身份。
func (account Account) PlayerID() PlayerID { return account.playerID }

// Username 返回 repository 使用的 canonical username。
func (account Account) Username() Username { return account.username }

// DisplayName 返回规范化展示值。
func (account Account) DisplayName() DisplayName { return account.displayName }

// Status 返回当前认证状态；调用方不得从 payload 覆盖该值。
func (account Account) Status() Status { return account.status }

// CreatedAt 返回持久化的 UTC 创建时间。
func (account Account) CreatedAt() time.Time { return account.createdAt }

// Valid 报告 Account 是否包含完整且可信的 repository 事实。
func (account Account) Valid() bool {
	return account.id.Valid() && account.playerID.Valid() && account.username.Valid() && account.displayName.Valid() && account.status.Valid() && !account.createdAt.IsZero()
}

// Summary 生成不会泄漏 username、player identity、status 或 credential 的公开投影。
// 返回值是不可变值副本，adapter 只能把显式 accessor 对应字段映射到公开协议。
func (account Account) Summary() AccountSummary {
	return AccountSummary{id: account.id, displayName: account.displayName, createdAt: account.createdAt}
}

// String 防止默认格式化展开账号内部身份和 canonical username。
func (Account) String() string { return "[REDACTED_ACCOUNT]" }

// GoString 防止 `%#v` 绕过普通 String 脱敏边界。
func (Account) GoString() string { return "[REDACTED_ACCOUNT]" }

// AccountSummary 是 register/login 成功后允许返回给 adapter 的安全账号投影。
type AccountSummary struct {
	// id 是公开账号关联值，不具备认证能力。
	id AccountID
	// displayName 是规范化后的公开展示值。
	displayName DisplayName
	// createdAt 是账号持久事实中的 UTC 创建时间。
	createdAt time.Time
}

// AccountID 返回公开账号身份。
func (summary AccountSummary) AccountID() AccountID { return summary.id }

// DisplayName 返回公开展示名。
func (summary AccountSummary) DisplayName() DisplayName { return summary.displayName }

// CreatedAt 返回账号创建时间。
func (summary AccountSummary) CreatedAt() time.Time { return summary.createdAt }

// Valid 报告 summary 是否来自完整 Account。
func (summary AccountSummary) Valid() bool {
	return summary.id.Valid() && summary.displayName.Valid() && !summary.createdAt.IsZero()
}

// validateIdentifier 限制服务端生成 ID 的前缀、长度与安全 ASCII 字符。
func validateIdentifier(value string, prefix string) error {
	if !strings.HasPrefix(value, prefix) || len(value) <= len(prefix) || len(value) > maximumIdentifierBytes {
		return errors.New("identifier prefix or length is invalid")
	}
	for index := len(prefix); index < len(value); index++ {
		if !asciiAlphaNumeric(value[index]) {
			return errors.New("identifier contains unsupported characters")
		}
	}
	return nil
}

// usernameCharacterAllowed 固定跨语言和数据库一致的登录字符集合。
func usernameCharacterAllowed(value byte) bool {
	return asciiAlphaNumeric(value) || value == '.' || value == '_' || value == '-'
}

// asciiAlphaNumeric 避免 locale 或 Unicode case rule 影响身份 key。
func asciiAlphaNumeric(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9'
}

// asciiLower 只转换协议允许的 ASCII uppercase，保持规范化规则可移植。
func asciiLower(value string) string {
	bytes := []byte(value)
	for index := range bytes {
		if bytes[index] >= 'A' && bytes[index] <= 'Z' {
			bytes[index] += 'a' - 'A'
		}
	}
	return string(bytes)
}

// LogValue 避免未来结构化日志展开 Account 的私有身份字段。
func (Account) LogValue() slog.Value { return slog.StringValue("[REDACTED_ACCOUNT]") }
