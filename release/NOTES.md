# Narbe v0.1.0-beta.1：首个公开测试版本

这是供 Owner 自行部署、接入真实云账号并测试的 Beta，不宣称生产稳定。

* 支持 AWS、Azure、Linode、DigitalOcean。
* 动态查询可用地区、服务器规格与系统镜像；支持已有服务器查询、创建与生命周期管理。
* Go + SQLite、内嵌中文Web、单个非root轻量容器，支持Linux AMD64和ARM64。无需服务器编译Go或引入MySQL/Redis。
* 六项独立审核问题已修复，应用基线 `5ffbf3479ad1e079b4b35efcaae837ecf08f2b3b` 的正式独立复审PASS，由Project + Product Manager本轮交付确认。
* 安装包使用Docker Compose，首次设置独立管理员密码，数据库和master.key保存在持久化Volume，默认仅绑定loopback。

真实四云创建、删除尚未完成E2E验收，Mock/HTTP合同/浏览器与Docker验证不能作为真实云成功证明。原CloudPanel完整功能覆盖仍有证据缺口。

**Azure删除VM后，关联磁盘和公网IP等资源可能继续计费。** 查看删除任务的账本和未知提示，并到Azure控制台逐项核实；Narbe不会自动扩大删除范围。

请优先使用SSH隧道或有效HTTPS反向代理，正确设置NARBE_ORIGIN。升级前备份包含数据库与master.key的完整Volume；不要执行down -v或全局prune。

安装与状态/日志/持久化/升级说明见安装包README或仓库 `release/README.md`。版本、构建SHA、镜像digest和发布工作流证据随本Release附件提供。原PR #1仍为Draft，main没有合并。
