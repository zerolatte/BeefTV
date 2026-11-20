[English](README.en.md) | 简体中文

# BeefTV

BeefTV 是面向 AI 影视和短剧创作的多用户 Web 工作台，提供项目、画布、素材和生成任务管理。管理员统一开户与配置 AI 模型，用户在各自独立工作区内创作，并使用管理员发放的整数积分。

## 快速开始

使用 Docker Compose 构建并初始化管理员：

```bash
docker compose build
docker compose run --rm -it backend platform-admin --data-dir /data/platform --username admin
docker compose up -d
```

打开 `http://localhost:3000`，登录管理员账号，在管理页面开户、发布模型价格与发放积分。初始化密码在终端交互输入，首次使用临时密码登录的用户必须改密。

## 部署与安全

- 平台使用单机 SQLite 和全新的 `platform-data` 卷，适合小规模独立工作区部署。不要复用旧单人数据目录。
- 公网必须配置 HTTPS 和 `CANVAS_PUBLIC_ORIGIN`；前端与 API 保持同源，后端端口仅留内网。
- 没有公开注册。管理员不能浏览他人的创作内容；模型密钥只在服务端管理。
- 生成前确认报价，提交冻结积分，按有效产出结算；不确定结果保留冻结，继续取回不创建新任务。
- 首版关闭内置助手、技能脚本、用户插件安装和外部 Agent；桌面发布入口已停用，原有单人数据保留。

## 文档

- [多用户平台与管理员操作](docs/content/docs/backend/platform.mdx)
- [本地开发](docs/content/docs/backend/local-development.mdx)
- [HTTP API](docs/content/docs/backend/http-api.mdx)
- [数据库](docs/content/docs/backend/backend-database.mdx)
- [完整文档目录](docs/index.md)
