# Go 代码规范

本项目的代码风格与规范要求。本文由上游 OpenIM 英文规范译出，并结合本项目实际情况做了修正（上游文档中已失效的命令已替换为当前可用的做法）。

## 1. 代码风格

### 1.1 代码格式

- 代码必须经 `gofmt` 格式化。
- 运算符与操作数之间留空格。
- 单行建议不超过 120 字符，超出请合理换行。例外场景：import 行、工具自动生成的代码、带 tag 的结构体字段。
- 单文件不超过 800 行。
- 单函数不超过 80 行。
- 所有代码必须经 `goimports` 整理（建议编辑器设置为保存时自动执行）。
- 禁止用相对路径导入包，如 `import ../util/net`。
- 包名与导入路径最后一级目录名不一致，或存在同名包冲突时，必须使用导入别名。

```go
// 不好
"github.com/dgrijalva/jwt-go/v4"

// 好
jwt "github.com/dgrijalva/jwt-go/v4"
```

- 导入包建议分组：标准库、第三方库、匿名导入、本项目内部包。匿名导入单独成组，并注明原因。

```go
import (
  // Go 标准库
  "fmt"

  // 第三方库
  "github.com/jinzhu/gorm"
  "github.com/spf13/cobra"

  // 匿名导入单独成组，并说明原因
  // 导入 mysql 驱动
  _ "github.com/jinzhu/gorm/dialects/mysql"

  // 本项目内部包
)
```

### 1.2 声明、初始化与定义

函数内需要多个变量时，可在函数开头用 `var` 集中声明。函数外的声明必须用 `var`，不要用 `:=`，否则容易踩变量作用域的坑。

```go
var (
  Width  int
  Height int
)
```

- 初始化结构体引用时用 `&T{}`，不要用 `new(T)`，与结构体初始化写法保持一致。

```go
// 不好
sptr := new(T)
sptr.Name = "bar"

// 好
sptr := &T{Name: "bar"}
```

- 结构体的声明与初始化都用多行格式。

```go
type User struct {
     Username string
     Email    string
}

user := User{
     Username: "belm",
     Email:    "nosbelm@qq.com",
}
```

- 同类声明要合并成组，常量、变量、类型声明同理。

```go
// 不好
import "a"
import "b"

// 好
import (
   "a"
   "b"
)
```

- 能预估容量时预分配内存。

```go
v := make(map[int]string, 4)
v := make([]string, 0, 4)
```

- 顶层声明用标准 `var` 关键字；除非类型与表达式类型不同，否则不写类型。

```go
// 不好
var s string = F()

// 好
var s = F()
// F 已明确返回 string，无需再写类型
```

- 导出常量用大驼峰（PascalCase），非导出用小驼峰（camelCase），不要全大写加下划线。

```go
// 不好
const (
    MAX_COUNT = 100
    timeout   = 30
)

// 好
const (
    MaxCount       = 100 // 导出常量用大驼峰
    defaultTimeout = 30  // 非导出常量用小驼峰
)
```

- 相关常量合并成组，便于阅读。

```go
// 不好
const apiVersion = "v1"
const retryInterval = 5

// 好
const (
    ApiVersion    = "v1"
    RetryInterval = 5
)
```

- 枚举用 `iota`，简洁且不易出错。

```go
// 不好
const (
    StatusActive   = 0
    StatusInactive = 1
    StatusUnknown  = 2
)

// 好
const (
    StatusActive = iota
    StatusInactive
    StatusUnknown
)
```

- 必要时显式写类型，并为导出常量补充注释。

```go
// 不好
const serverAddress = "localhost:8080"
const debugMode = 1 // 这是布尔还是整数？

// 好
const ServerAddress string = "localhost:8080"

// DebugMode 表示应用是否以调试模式运行。
const DebugMode bool = true
```

- context 的 key 使用自定义类型，避免字符串 key 冲突和误用。

```go
// 不好：字符串 key 容易拼错、容易与其他包冲突
const userIDKey = "userID"

// 好
type contextKey string

const userIDKey contextKey = "userID"
```

- 嵌入类型（如 mutex）放在结构体字段列表最前面，且与普通字段之间空一行。

```go
// 不好
type Client struct {
   version int
   http.Client
}

// 好
type Client struct {
   http.Client

   version int
}
```

### 1.3 错误处理

- 函数返回的 `error` 必须处理，或显式赋值忽略。`defer xx.Close()` 无需显式处理。

```go
// 不好
load()

// 好
_ = load()
```

- 多返回值时，`error` 必须是最后一个返回值。

```go
// 不好
func load() (error, int)

// 好
func load() (int, error)
```

- 尽早处理错误、尽早返回，减少嵌套。

```go
// 不好
if err != nil {
// 错误处理
} else {
// 正常逻辑
}

// 好
if err != nil {
// 错误处理
return err
}
// 正常逻辑
```

- 若要在 `if` 外使用函数返回值，先赋值再判断。

```go
// 不好
if v, err := foo(); err != nil {
// 错误处理
}

// 好
v, err := foo()
if err != nil {
// 错误处理
}
```

- 错误判断要独立，不要与其他逻辑混在同一个条件里。

```go
// 不好
v, err := foo()
if err != nil || v == nil {
  return err
}

// 好
v, err := foo()
if err != nil {
  return err
}

if v == nil {
  return errors.New("invalid value v")
}
```

- 错误描述以小写字母开头，结尾不加标点。

```go
// 不好
errors.New("Redis connection failed")
errors.New("redis connection failed.")

// 好
errors.New("redis connection failed")
```

- 错误文案写「能做什么」，而不是「不能做什么」；声明要求用 must 而非 should；声明格式错误用 must not；声明不可执行的动作可用 may not；引用字面量用单引号；引用字段名用反引号；表示大小关系用文字而非符号（`must be less than 256`）；表示范围尽量用闭区间。
- Go 1.13 及以上建议用 `fmt.Errorf("module xxx: %w", err)` 包装错误。

### 1.4 panic 处理

`panic` 的使用必须严格受控，保证程序稳定、错误处理可预期。

- **业务逻辑中禁止使用**：业务处理一律通过返回 error 向上传递问题。
- **main 包中受限使用**：仅在程序完全无法运行时（关键文件打不开、数据库连不上等启动期致命错误）才考虑，即便如此也优先用结构化的错误退出。
- **对外导出的接口禁止 panic**：导出接口应以返回 error 作为契约。
- **优先用 error 而非 panic**：包内传错一律用 error。

#### panic 的替代方案：结构化退出

```go
// ExitWithError 打印错误并以非零状态码退出。
func ExitWithError(err error) {
	progName := filepath.Base(os.Args[0])
	fmt.Fprintf(os.Stderr, "%s exit -1: %+v\n", progName, err)
	os.Exit(-1)
}

// SIGTERMExit 在收到 SIGTERM 时打印警告并以 0 退出。
func SIGTERMExit() {
	progName := filepath.Base(os.Args[0])
	fmt.Fprintf(os.Stderr, "Warning %s receive process terminal SIGTERM exit 0\n", progName)
}
```

用法：

```go
func main() {
	apiCmd := cmd.NewApiCmd()
	apiCmd.AddPortFlag()
	apiCmd.AddPrometheusPortFlag()
	if err := apiCmd.Execute(); err != nil {
		util.ExitWithError(err)
	}
}
```

### 1.5 单元测试

- 测试文件命名为 `xxx_test.go`。
- 每个重要的导出函数都要有测试用例。
- 测试文件不对外暴露，可直接测试导出结构体与函数。
- 若被测方法是 `func (b *Bar) Foo`，测试函数命名为 `func TestBar_Foo`。

### 1.6 类型断言失败处理

- 单返回值的类型断言在类型不符时会 panic，必须使用 "comma ok" 写法。

```go
// 不好
t := n.(int)

// 好
t, ok := n.(int)
if !ok {
// 错误处理
}
```

## 2. 命名规范

统一、简短、精确的命名能显著提升可读性，并避免不必要的 bug。

### 2.1 包命名

- 包名必须与目录名一致，尽量简短且有意义，不得与标准库冲突。
- 包名全小写，不用大写与下划线；用多级目录划分层级。
- 项目名可用连字符连接多个单词。
- 包名与所在目录名不用复数，例如 `net/url` 而非 `net/urls`。
- 不要用 `common`、`util`、`shared`、`lib` 这类宽泛无意义的名字。
- 包名要简洁明确，如 `net`、`time`、`log`。

### 2.2 函数命名

- 函数名用驼峰。导出函数首字母大写（`MixedCaps`），内部函数首字母小写（`mixedCaps`）。
- 例外：工具生成的代码（如 `xxx.pb.go`）、以及为提高可读性用下划线的测试函数（如 `TestMyFunction_WhatIsBeingTested`）。

### 2.3 文件与目录命名

**文件名：**
- 默认用下划线（`_`）分隔，保持简短、可表意。
- 连字符（`-`）与下划线都允许，一般优先下划线。

**脚本与 Markdown：**
- shell 脚本与 Markdown 文件优先用连字符（`-`），便于搜索与网页兼容。

**目录名：**
- 目录名只用连字符（`-`）分隔单词。

文件名一律小写，用有意义且简洁的标识，便于组织与检索。

### 2.4 结构体命名

- 用驼峰，按访问权限决定首字母大小写，如 `MixedCaps` 或 `mixedCaps`。
- 结构体名用名词而非动词，如 `Node`、`NodeSpec`。
- 避免 `Data`、`Info` 这类无意义的名字。
- 结构体的声明与初始化都用多行格式。

```go
// 多行声明
type User struct {
     name  string
     Email string
}

// 多行初始化
u := User{
     UserName: "belm",
     Email:    "nosbelm@qq.com",
}
```

### 2.5 接口命名

- 与结构体命名规则基本一致。
- 单方法接口用「方法名 + er」命名（如 `Reader`、`Writer`），有时英文会别扭，但可以接受。
- 两个方法的接口用两个方法名组合，如 `ReadWriter`。
- 三个方法以上的接口，按结构体命名方式处理。

```go
// Seeker 是对偏移量定位能力的抽象。
type Seeker interface {
  Seek(offset int64, whence int) (int64, error)
}

// ReadWriter 组合了基础的 Read 与 Write 方法。
type ReadWriter interface {
  Reader
  Writer
}
```

### 2.6 变量命名

- 变量名用驼峰，首字母大小写由访问权限决定。
- 上下文简单、指向明确的场景可以缩写：`user` → `u`，`userID` → `uid`。
- 专有名词规则：
  - 变量为私有且专有名词是首词时用小写，如 `apiClient`。
  - 其他情况保留名词原写法，如 `APIClient`、`repoID`、`UserID`。

常见专有名词首字母缩写表（参考 golang/lint）：

```go
var LintGonicMapper = GonicMapper{
     "API": true, "ASCII": true, "CPU": true, "CSS": true, "DNS": true,
     "EOF": true, "GUID": true, "HTML": true, "HTTP": true, "HTTPS": true,
     "ID": true, "IP": true, "JSON": true, "LHS": true, "QPS": true,
     "RAM": true, "RHS": true, "RPC": true, "SLA": true, "SMTP": true,
     "SSH": true, "TLS": true, "TTL": true, "UI": true, "UID": true,
     "UUID": true, "URI": true, "URL": true, "UTF8": true, "VM": true,
     "XML": true, "XSRF": true, "XSS": true,
}
```

- 布尔变量以 `Has`、`Is`、`Can`、`Allow` 开头。

```go
var hasConflict bool
var isExist     bool
var canManage   bool
var allowGitHook bool
```

- 局部变量尽量短，如 `buf` 表示 buffer，`i` 表示 index。
- 工具生成的代码可不受此约束（如 `xxx.pb.go` 中的 `Id`）。

### 2.7 常量命名

- **驼峰命名**：常量名用驼峰，首字母大小写由访问权限决定。大写表示导出，小写表示包内可见。
- **枚举型常量**：表示一组枚举值时，先定义对应类型，既提升类型安全，也让用途更清晰。

```go
// Code 定义错误码类型。
type Code int

// 内部错误。
const (
     // ErrUnknown - 0：未知错误。
     ErrUnknown Code = iota
     // ErrFatal - 1：致命错误。
     ErrFatal
)
```

### 2.8 跨包的全局变量与常量

- **尽量用常量代替全局变量**：需要跨包访问时优先定义为常量，保证不可变，避免被意外修改。
- **包内使用则小写**：只在包内使用的全局量以小写字母开头，明确其作用域。

```go
package config

// MaxConnections 允许的最大连接数，跨包可见。
const MaxConnections int = 100

// minIdleTime 连接被判为陈旧前的最小空闲时间，仅包内可见。
const minIdleTime int = 30
```

### 2.9 错误命名

- 错误类型写成 `FooError`。

```go
type ExitError struct {
// ....
}
```

- 错误变量写成 `ErrFoo`。

```go
var ErrFormat = errors.New("unknown format")
```

不符合该命名的写法会被 CI 拦下。

### 2.10 正确处理错误

#### 原则：不要丢弃错误

调用返回 error 的函数时，调用方必须处理或向上传递，不允许静默忽略。

**反例：丢弃错误**

```go
func ReadFileContent(filename string) string {
	content, _ := ioutil.ReadFile(filename) // 错误：忽略了 error
	return string(content)
}
```

文件不存在或无法读取时，程序仍会继续执行，最终在下游抛出更难理解的错误。

**正例：向上传递错误**

```go
// ReadFileContent 读取并返回指定文件的内容，读取失败时返回 error。
func ReadFileContent(filename string) (string, error) {
	content, err := ioutil.ReadFile(filename)
	if err != nil {
		return "", err
	}
	return string(content), nil
}

func main() {
	content, err := ReadFileContent("example.txt")
	if err != nil {
		log.Fatalf("读取文件失败：%v", err)
	}
	log.Println(content)
}
```

#### 错误处理最佳实践

1. 始终检查函数返回的 error，不要忽略。
2. 除非当前层能优雅处理，否则把错误向上传递。
3. 传递时补充上下文，便于追溯来源（用 `fmt.Errorf` 的 `%w`，或用 `errs.WrapMsg`）。
4. 在「被处理」或「导致程序终止」的位置记录日志。

### 2.11 IO / IPC 场景使用 context

`context.Context` 用于管理超时、取消信号以及请求级的值传递，在 I/O 与进程间通信中尤其重要。

**反例：HTTP 调用未使用 context**

```go
// FetchData 发起 HTTP GET 请求并返回响应体。
// 该函数未使用 context，无法取消请求或设置超时。
func FetchData(url string) (string, error) {
	resp, err := http.Get(url) // 错误：未使用 context
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return string(body), nil
}
```

服务端迟迟不响应时，请求无法中止，会造成资源浪费。

**正例：HTTP 调用使用 context**

```go
// FetchDataWithContext 使用传入的 context 发起 HTTP GET 请求，
// 可依据 context 的 deadline 取消或超时。
func FetchDataWithContext(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return string(body), nil
}

func main() {
	// 创建 5 秒超时的 context
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	data, err := FetchDataWithContext(ctx, "http://example.com")
	if err != nil {
		log.Fatalf("获取数据失败：%v", err)
	}
	log.Println(data)
}
```

#### context 使用最佳实践

1. context 作为函数第一个参数：`func(ctx context.Context, ...)`。
2. 支持 context 的函数不得忽略它，必须在 I/O 或 IPC 中使用。
3. 不要把 context 存进结构体，它应在调用链中传递。
4. 用取消与超时机制控制阻塞操作的生命周期，网络 I/O 尤为重要。
5. 把 context 一路向下传递，保证整条链路都能响应取消与超时。

## 3. 注释规范

- 每个导出的名字都必须有注释，简要说明导出的变量、函数、结构体、接口等。
- 一律使用单行注释，禁止多行注释（`/* */`）。
- 单行注释同样不超过 120 字符，超出请换行并保持格式整齐。
- 注释必须是完整句子，以被注释对象开头、以句号结尾，格式为 `// 名称 描述`。

```go
// 不好
// logs the flags in the flagset.
func PrintFlags(flags *pflag.FlagSet) {
// 正常代码
}

// 好
// PrintFlags 打印 flagset 中的各个 flag。
func PrintFlags(flags *pflag.FlagSet) {
// 正常代码
}
```

- 提交代码评审前，注释掉的代码必须删除；确需保留的，要说明原因与后续处理方案。
- 多段注释之间可用空行分隔。

```go
// Package superman 实现拯救世界的方法。
//
// 经验表明，少量流程在拯救世界时会非常有用。
package superman
```

### 3.1 包注释

- 每个包有且只有一个包级注释。
- 统一用 `//`，格式为 `// Package <包名> 包描述`。

```go
// Package genericclioptions 提供可挂到命令上、可绑定、可补全并生成辅助函数的 flag。
package genericclioptions
```

### 3.2 变量/常量注释

- 每个导出变量/常量都要有注释，格式为 `// 变量名 变量描述`。

```go
// ErrSigningMethod 表示签名方法无效的错误。
var ErrSigningMethod = errors.New("invalid signing method")
```

- 大段常量/变量定义时，可先写总述，再在每行常量上方或行尾逐一说明。

```go
// Code 必须以 1xxxxx 开头。
const (
     // ErrSuccess - 200：成功。
     ErrSuccess int = iota + 100001

     // ErrUnknown - 500：服务内部错误。
     ErrUnknown

     // ErrBind - 400：请求体绑定到结构体时出错。
     ErrBind

     // ErrValidation - 400：参数校验失败。
     ErrValidation
)
```

### 3.3 结构体注释

- 每个需要导出的结构体或接口都要有注释，格式为 `// 结构体名 结构体描述。`
- 导出结构体中的成员变量，含义不直观时必须注释，写在字段上方或同行行尾。

```go
// User 表示一个用户资源，同时作为 gorm 模型使用。
type User struct {
     // 标准对象元数据。
     metav1.ObjectMeta `json:"metadata,omitempty"`

     Nickname string `json:"nickname" gorm:"column:nickname"`
     Password string `json:"password" gorm:"column:password"`
     Email    string `json:"email" gorm:"column:email"`
     Phone    string `json:"phone" gorm:"column:phone"`
     IsAdmin  int    `json:"isAdmin,omitempty" gorm:"column:isAdmin"`
}
```

### 3.4 方法注释

每个导出的函数或方法都要有注释，格式为 `// 函数名 函数描述。`

```go
// BeforeUpdate 在更新数据库记录之前执行。
func (p *Policy) BeforeUpdate() (err error) {
// 正常代码
  return nil
}
```

### 3.5 类型注释

- 每个需要导出的类型定义与类型别名都要有注释，格式为 `// 类型名 类型描述。`

```go
// Code 定义错误码类型。
type Code int
```

## 4. 类型

### 4.1 字符串

- 空字符串判断。

```go
// 不好
if s == "" {
}

// 好
if len(s) == 0 {
}
```

- `[]byte` / `string` 相等比较。

```go
// 不好
bytes.Equal(s1, s2) == 0
bytes.Equal(s1, s2) != 0

// 好
bytes.Compare(s1, s2) == 0
bytes.Compare(s1, s2) != 0
```

- 含转义字符的复杂字符串用原始字符串（反引号），避免层层转义。

```go
// 不好
regexp.MustCompile("\\.")

// 好
regexp.MustCompile(`\.`)
```

### 4.2 切片

- 空切片判断（map、channel 同理）。

```go
// 不好
if len(slice) == 0 {
}

// 好
if slice != nil && len(slice) == 0 {
}
```

- 声明切片。

```go
// 不好
s := []string{}
s := make([]string, 0)

// 好
var s []string
```

- 切片拷贝。

```go
// 不好
for i, v := range b1 {
    b2[i] = v
}

// 好
copy(b2, b1)
```

- 切片追加。

```go
// 不好
for _, v := range a {
     b = append(b, v)
}

// 好
b = append(b, a...)
```

### 4.3 结构体

- 结构体初始化用多行格式，字段名显式写出。

```go
type user struct {
     Id   int64
     Name string
}

u1 := user{100, "Colin"} // 不好：位置依赖，字段变动会出错

u2 := user{              // 好
     Id:   200,
     Name: "Lex",
}
```

## 5. 控制结构

### 5.1 if

- `if` 支持初始化语句，本地变量建议这样创建。

```go
if err := loadConfig(); err != nil {
// 错误处理
return err
}
```

- `bool` 变量直接判断，不要与 `true`/`false` 比较。

```go
var isAllow bool
if isAllow {
// 正常代码
}
```

### 5.2 for

- 用短声明创建循环内变量。

```go
sum := 0
for i := 0; i < 10; i++ {
     sum += 1
}
```

- 不要在 for 循环体内用 `defer`——`defer` 只在函数返回时执行，循环中会堆积资源不释放。

```go
// 不好
for file := range files {
    fd, err := os.Open(file)
    if err != nil {
        return err
    }
    defer fd.Close()
    // 正常代码
}

// 好：用立即执行的匿名函数限定 defer 的作用域
for file := range files {
    func() {
        fd, err := os.Open(file)
        if err != nil {
            return
        }
        defer fd.Close()
        // 正常代码
    }()
}
```

### 5.3 range

- 只要 key，省略第二个值。

```go
for keyIndex := range keys {
// 正常代码
}
```

- 只要 value，第一个值用下划线。

```go
sum := 0
for _, value := range array {
     sum += value
}
```

### 5.4 switch

- 必须有 `default`。

```go
switch os := runtime.GOOS; os {
     case "linux":
         fmt.Println("Linux.")
     case "darwin":
         fmt.Println("OS X.")
     default:
         fmt.Printf("%s.\n", os)
}
```

### 5.5 goto

- 业务代码禁止使用 `goto`。
- 框架或底层源码中也尽量不用。

## 6. 函数

- 入参与返回值首字母小写。
- 参数个数不超过 5 个。
- 函数按大致调用顺序排列；同一文件内的函数按 receiver 分组。
- 尽量用值传递而非指针传递。
- 入参是 map、slice、chan、interface 时，不要传指针。

### 6.1 函数参数

- 返回两三个同类型参数，或返回值的含义从上下文看不出来时，使用命名返回值；否则不建议使用。

```go
func coordinate() (x, y float64, err error) {
// 正常代码
}
```

- 返回值最多三个，超过三个请用结构体。

### 6.2 defer

- 资源创建后应立即 `defer` 释放（Go 1.14 起 defer 性能已大幅优化，即使在性能敏感业务中也可放心使用）。
- 先判断错误，再 defer 释放资源。

```go
resp, err := http.Get(url)
if err != nil {
     return err
}

defer resp.Body.Close()
```

### 6.3 方法接收者

- 建议用类型名首字母的小写作为接收者名。
- 函数超过 20 行时，不要用单字符作为接收者名。
- 接收者名不得使用 `me`、`this`、`self` 这类容易混淆的名字。

### 6.4 嵌套

- 嵌套层级不超过 4 层。

### 6.5 变量命名

- 变量声明尽量靠近首次使用处（就近原则）。
- 同一个魔法数字出现两次以上，禁止直接使用，必须定义常量。

```go
// Price 单价。
const Price = 3.14

func getAppleCost(n float64) float64 {
     return Price * n
}

func getOrangeCost(n float64) float64 {
     return Price * n
}
```

## 7. GOPATH 与依赖管理

- Go 1.11 之后 GOPATH 规则已弱化，存量代码建议继续遵守以便维护。
- 建议只设置一个 GOPATH；设置多个时，编译产物落在第一个 GOPATH 的 bin 目录下。
- Go 1.11 以上必须使用 Go Modules。
- 使用 Go Modules 时不建议提交 vendor 目录。
- 使用 Go Modules 时必须提交 `go.sum`。

## 8. 最佳实践

- 尽量少用全局变量，多通过参数传递，让函数「无状态」，降低耦合、便于分工与单测。
- 在编译期校验接口实现，例如：

```go
type LogHandler struct {
   h   http.Handler
   log *zap.Logger
}

var _ http.Handler = LogHandler{}
```

- 服务端处理请求时应创建 context，把请求相关信息（如 requestID）存入并在调用链中传递。

### 8.1 性能

- `string` 是不可变类型，修改 string 需要重新申请内存，代价较高；需要频繁修改时优先用 `[]byte`。
- 数字与字符串互转优先用 `strconv`，不要用 `fmt`。

### 8.2 注意事项

- `append` 可能重新分配底层数组并返回新地址，注意返回值必须接住。
- 想直接修改 map 中的值，value 必须是指针，否则只能整体覆盖。
- map 并发读写必须加锁。
- `interface{}` 的类型转换编译期无法检查，只在运行期生效，注意避免 panic。

## 9. golangci-lint

golangci-lint 是 Go 的 linter 聚合器，并行执行、带缓存，适配 CI 环境。

> 注意：上游文档中的 `make lint` 在本项目**不存在**（本项目没有 Makefile）。如需本地检查，请自行安装后直接运行：
>
> ```bash
> golangci-lint run
> ```

配置文件为项目根目录的 `.golangci.yml`，可参考官方文档选择 linter：<https://golangci-lint.run/usage/linters/>

本项目启用的 linter（`linters.enable`）：

```yaml
linters:
  disable-all: true
  enable:
    - typecheck     # 基础类型检查
    - gofmt         # 格式检查
    - govet         # Go 官方静态检查
    - gosimple      # 代码简化建议
    - misspell      # 拼写错误
    - staticcheck   # 静态分析
    - unused        # 未使用代码
    - goimports     # import 排序与格式
    - godot         # 注释标点检查
    - bodyclose     # HTTP 响应体是否关闭
    - errcheck      # 未处理的错误返回
  fast: true
```

### 9.1 运行参数

- `concurrency`：并发数，默认取可用 CPU 数。
- `timeout`：分析超时时间，默认 1 分钟。
- `issues-exit-code`：发现问题时的退出码，默认 1。
- `tests`：是否包含测试文件，默认 true。
- `build-tags`：所有 linter 共用的构建标签。
- `skip-dirs` / `skip-files`：跳过指定目录/文件（支持正则）。

### 9.2 输出配置

- `format`：输出格式，默认 `colored-line-number`。
- `print-issued-lines`：是否打印问题所在行，默认 true。
- `print-linter-name`：是否在问题末尾打印 linter 名称，默认 true。
- `uniq-by-line`：同一行是否只报一次，默认 true。
- `path-prefix`：输出路径前缀，默认无。
- `sort-results`：按文件、行、列排序结果。

### 9.3 常用 linter 说明

- **bidichk**：检查双向文本字符，避免左右文混排时显示方向错乱。
- **dogsled**：检查赋值中 `_` 的过度使用（会掩盖错误或让逻辑不清）。
- **dupl**：识别重复代码块，`threshold` 可调整判定相似度。
- **errcheck**：检查未处理的错误返回。
- **exhaustive**：检查 `switch` 是否覆盖枚举的全部取值。

### 9.4 示例：errcheck

**错误写法**

```go
func main() {
    f, _ := os.Open("filename.ext") // 忽略了 error
    defer f.Close()
}
```

`os.Open` 的错误被显式丢弃，文件打不开时会 panic 在 nil 指针上，问题难排查。

**正确写法**

```go
func main() {
    f, err := os.Open("filename.ext")
    if err != nil {
        fmt.Printf("打开文件失败：%v\n", err)
        return
    }
    defer f.Close()
}
```

### 9.5 示例：gofmt

**错误写法**

```go
package main
import "fmt"
func main() {
fmt.Println("Hello, world!")
}
```

**正确写法**

```go
package main

import "fmt"

func main() {
    fmt.Println("Hello, world!")
}
```

`gofmt` 可自动修复这类格式问题。

### 9.6 示例：unused

**错误写法**

```go
func helper() {}

func main() {}
```

`helper` 定义后从未被调用，说明是冗余代码或功能未完成。

**正确写法**

```go
// 若 helper 确实需要，确保它被正确使用。
func helper() {
    // 实现功能，或在别处调用
}

func main() {
    helper()
}
```

### 9.7 示例：dogsled

```go
func getValues() (int, int, int) {
    return 1, 2, 3
}

func main() {
    _, _, val := getValues() // 两个下划线掩盖了前两个返回值
    fmt.Println(val)
}
```

过多使用空标识符会让代码难以阅读，建议重构函数返回值或显式注释说明为何可以忽略。

### 9.8 示例：exhaustive

**错误写法**

```go
type Fruit int

const (
    Apple Fruit = iota
    Banana
    Orange
)

func getFruitName(f Fruit) string {
    switch f {
    case Apple:
        return "Apple"
    case Banana:
        return "Banana"
    // 缺少 Orange 分支
    }
    return "Unknown"
}
```

**正确写法**

```go
func getFruitName(f Fruit) string {
    switch f {
    case Apple:
        return "Apple"
    case Banana:
        return "Banana"
    case Orange:
        return "Orange"
    }
    return "Unknown"
}
```

补上缺失的 `case` 后，`switch` 覆盖了枚举的所有取值。

---

通过上述规范与工具，可以在编码阶段就拦截大部分低级问题，减少 bug、提升可维护性，也让代码评审更聚焦在设计与逻辑上。
