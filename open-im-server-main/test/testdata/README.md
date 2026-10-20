
# OpenIM Server 测试数据

本目录（`testdata`）存放用于测试 OpenIM Server 的各类 JSON 格式数据文件。

## 目录结构

```bash
testdata/
│
├── README.md         # 描述该目录下各子目录和文件的作用
│
├── storage/              # 存储模拟的数据库数据
│   ├── users.json   # 用户的模拟数据
│   └── messages.json # 消息的模拟数据
│
├── requests/        # 存储模拟的请求数据
│   ├── login.json   # 模拟登陆请求
│   ├── register.json # 模拟注册请求
│   └── sendMessage.json # 模拟发送消息请求
│
└── responses/       # 存储模拟的响应数据
    ├── login.json   # 模拟登陆响应
    ├── register.json # 模拟注册响应
    └── sendMessage.json # 模拟发送消息响应
```

各子目录与文件的作用说明如下：

- `db/` —— 存放模拟真实数据库内容的 mock 数据。
  - `users.json` —— 系统中的用户列表，每条记录包含用户 ID、用户名、密码哈希等信息。
  - `messages.json` —— 用户之间往来的消息列表，每条记录包含收发双方的用户 ID、消息内容、时间戳等信息。
- `requests/` —— 存放客户端可能发给服务端的模拟请求。
  - `login.json` —— 用户登录请求，包含用户名、密码等字段。
  - `register.json` —— 用户注册请求，包含用户名、密码、邮箱等信息。
  - `sendMessage.json` —— 用户向其他用户发送消息的模拟请求。
- `responses/` —— 存放与上述请求对应的预期服务端响应。
  - `login.json` —— 登录成功的响应，通常包含会话 token 与用户信息。
  - `register.json` —— 注册成功的响应，通常包含新用户的 ID、用户名等。
  - `sendMessage.json` —— 发送消息成功的响应，表示消息已被接收。

## JSON 格式

本目录下所有数据文件均为 JSON 格式。JSON（JavaScript Object Notation）是一种轻量级的数据交换格式，便于人阅读与编写，也便于机器解析与生成。

下面是一个简单的 JSON 文件示例：

```bash
  "users": [
    {
      "id": 1,
      "username": "user1",
      "password": "password1"
    },
    {
      "id": 2,
      "username": "user2",
      "password": "password2"
    }
  ]

```

上例中，`users` 是一个用户对象数组，每个用户对象包含 `id`、`username` 和 `password`。
