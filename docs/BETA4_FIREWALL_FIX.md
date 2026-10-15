# beta.4 Linode 防火墙空集合定点修复（待应用专项复审）

应用正式审核基线 e22627ee181dac1d754a11d9a45ca5376f0d55a9；旧发行候选168dde3b8ab67ca3443201621b09403df3c880ca。Owner beta.3报错在HTTP Chromium真实复现：`Cannot read properties of null (reading 'length')`。本次不改变四云初始化业务、不操作真实防火墙或Owner环境。

## A：应用审核边界

先完整保存168dde3相对e226的binary patch及既有验证证据，再正常提交恢复发行内容：`2f6d44d8770d7b65b89a8e954919972a36daacb3`，该提交整棵Git树与e226相同。没有reset/force push，没有删除历史。本文所在应用候选的release/及Workflow内容必须与e226完全相同，不能夹带未经审定的beta.4发行脚本。

本次应用Diff只包含 `internal/web/assets/linode-ui.js`、局部CSS、`internal/cloud/linode_read.go`、相应Go/浏览器测试和本说明/截图。startup业务源码保持；startup浏览器替身仅补上真实firewall API本来会返回的snapshot字段。新浏览器由Go TestBrowser命名自动纳入既有CI，不改CI配置。

## 根因与实际行为

`pages`接收到明确的空data数组时，内部聚合nil Slice再序列化/解码使设备或列表成为nil；Go JSON输出null。规则方向本身也允许官方响应为null。前端直接length/map/for-of，旧浏览器替身只给非空/[]，未覆盖真实Go序列化边界。

- 在Linode只读DTO出口创建显示副本，明确空devices/items/inbound/outbound输出[]。原始l.firewall、分页完整性、fingerprint、写前复核不变；原始快照先算后透传，不根据UI归一化重算。上游分页data:null或缺失仍不等于确认零资源，不放宽成全部放行。
- 前端只将显式null集合归一化；Array.isArray严格拒绝对象/字符串/缺失集合。ID、label、status、snapshot、策略、规则与设备实体/接口父归属错误时给出中文错误，不当成允许策略或零绑定。
- 创建、克隆/调整服务器绑定、账户列表、规则编辑共同读取已校验数据。数据尚未核实或畸形时readFirewallSelection拒绝提交；原权限不足时的显式人工核实选项保留。没有更改默认legacy_config/firewall_id=-1、全局默认、实际绑定或写API。
- 空设备显示0个绑定，默认DROP/ACCEPT按实际值显示和编辑，不凭空生成放行规则；共享多绑定继续禁止直接删除。局部`.firewall-picker [hidden]`修复复选框因作者CSS覆盖hidden仍可见的问题，不改变其他页面样式。

## 红→绿及实际验证

HTTP浏览器最小用例先失败并显示与Owner相同的null.length错误，桌面和手机截图已保存。后端测试先失败于nil输出、items:null及畸形状态未拒绝，修复后通过。

新的真实Go httptest Handler + Session/CSRF + SQLite + LinodeAdvanced替身向浏览器提供JSON；没有Playwright API fulfillment。覆盖devices=null/[]/多项、items=null、两方向单独/同时null、合法默认策略无规则、畸形ID/状态/设备/策略/规则/集合，真实规则编辑/创建/legacy与Linode Interfaces绑定确认、默认全部放行创建。5个confirm/jobs走真实API入队，再真实取消queued；断言provider写调用0，不启动实际云任务。

完整race、vet、五组桌面/手机浏览器通过：原beta.3九能力、beta.4四云脚本与新防火墙回归均保留。Go合同验证显示副本不改变已有快照，且固定修复前指纹`81cd5fbcf81e85fe10da0caded6d0a90d6a1bdce01c19b778255054976d1b8f3`不变，现有共享影响范围变化拒写合同继续通过。格式及纯JS向量检查通过。

本轮agy Embedded Reviewer 1次（gemini-3.8-flash-high/high，180s），提供精简Diff/代码/真实测试Evidence Pack，不让其搜索仓库或.git；返回PASS。Engineer核对其未知设备类型、权限文案依赖风险均为保守失败关闭。内部PASS不替代A/B两组正式复审。

Docker使用相同固定基础镜像，本机构建及beta.3持久化升级结果、最终Push/PR CI链接由最终交付报告实读提供；不能把本说明写入时尚未完成的步骤当PASS。升级只使用disposable Volume，保留beta.3回滚镜像，不全局prune。

## B：发行审核边界与停止点

应用候选形成后，重新应用保存的beta.4发行补丁，唯一应用锚点更新为该新应用候选，形成新的发行候选。A审核只看恢复提交→应用候选；B审核只看新应用候选→新发行候选，不复用168dde3为批准值。版本发布后文案白名单、双SHA、权限和原生两架构/回拉/升级保持，失败关闭测试必须重新执行。

两组正式Independent Reviewer均PASS及上游明确发行授权前：不设置任何BETA4审核变量，不建Tag/Release、不推GHCR、不合并main、不改Owner beta.3部署。两个旧审核PASS不覆盖本修复或新发行基线。

## 截图（真实HTTP浏览器；全部为测试替身数据）

| 场景 | 桌面 | 手机 |
|---|---|---|
| 修复前null异常 | [桌面](images/beta4-firewall/before-desktop.png) | [手机](images/beta4-firewall/before-mobile.png) |
| 修复后零绑定 | [桌面](images/beta4-firewall/firewall-null-fixed-desktop.png) | [手机](images/beta4-firewall/firewall-null-fixed-mobile.png) |
| 空规则编辑 | [桌面](images/beta4-firewall/firewall-rules-empty-desktop.png) | [手机](images/beta4-firewall/firewall-rules-empty-mobile.png) |
| 多绑定影响范围 | [桌面](images/beta4-firewall/firewall-multiple-bindings-desktop.png) | [手机](images/beta4-firewall/firewall-multiple-bindings-mobile.png) |
| 空列表 | [桌面](images/beta4-firewall/firewall-empty-list-desktop.png) | [手机](images/beta4-firewall/firewall-empty-list-mobile.png) |
| 畸形数据拒绝 | [桌面](images/beta4-firewall/firewall-malformed-blocked-desktop.png) | [手机](images/beta4-firewall/firewall-malformed-blocked-mobile.png) |
| 创建已有FW选择 | [桌面](images/beta4-firewall/firewall-create-existing-desktop.png) | [手机](images/beta4-firewall/firewall-create-existing-mobile.png) |
| legacy绑定 | [桌面](images/beta4-firewall/firewall-bind-null-desktop.png) | [手机](images/beta4-firewall/firewall-bind-null-mobile.png) |
| Linode Interfaces绑定 | [桌面](images/beta4-firewall/firewall-bind-interfaces-desktop.png) | [手机](images/beta4-firewall/firewall-bind-interfaces-mobile.png) |
