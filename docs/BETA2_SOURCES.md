# beta.2 信息来源与设计边界

资料核对：2026-10-14。地区翻译仅显示，不修改原始 ID。新地区/无法确认的位置保留官方名称和代码，不按代码前缀猜城市；目录是实时可访问地区，不因本地翻译表覆盖不足而隐藏新地区。

## 官方资料

- AWS地区及代码：https://docs.aws.amazon.com/global-infrastructure/latest/regions/aws-regions.html
- AWS启用地区API：https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_DescribeRegions.html
- AWS规格、vCPU、MiB内存与本地实例存储：https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_DescribeInstanceTypes.html
- Azure地理位置与programmatic name：https://learn.microsoft.com/en-us/azure/reliability/regions-list
- Azure Compute SKUs：https://learn.microsoft.com/en-us/rest/api/compute/resource-skus/list
- Azure资源组VM范围：https://learn.microsoft.com/en-us/rest/api/compute/virtual-machines/list
- Linode公开地区API（本轮实际只读查询33条）：https://api.linode.com/v4/regions?page_size=500
- Linode规格、MB单位、USD小时/月价格及地区覆盖价格：https://techdocs.akamai.com/linode-api/reference/get-linode-type
- Linode实例specs/IP字段与权限范围：https://techdocs.akamai.com/linode-api/reference/get-linode-instances
- DigitalOcean数据中心及代码：https://docs.digitalocean.com/platform/regional-availability/
- DigitalOcean规格、内存MB、磁盘GB、USD小时/月价格：https://docs.digitalocean.com/reference/api/reference/sizes/
- DigitalOcean账号级Droplets：https://docs.digitalocean.com/reference/api/reference/droplets/
- Web Crypto HTTP限制：https://developer.mozilla.org/en-US/docs/Web/API/Crypto/randomUUID
- HTTP可用的CSPRNG：https://developer.mozilla.org/en-US/docs/Web/API/Crypto/getRandomValues

## 产品与实现约束

成品工作台的局部增强：普通用户从云账号进入服务器总览，先判断资源数量与数据完整性，再按地区/状态/名称/IP定位；创建使用现有表单，重点配置在前、高级设置折叠，提交前展示摘要，危险操作仍名称确认。桌面/手机均保留搜索、筛选、刷新、错误及重试。沿用当前CSS/原生模块，不引入图表或前端运行时。

账号总览异步只读扫描：进程内最多8个缓存账号、全局2个扫描；AWS每扫描3个并发地区，每地区15秒，整体90秒，最多100个地区和5000条展示实例；60秒缓存，手动刷新至少间隔15秒。缓存绑定account ID和凭据revision，不写入本地任务代替真实资源。失败地区可单独重试；账号级接口失败需重试账号范围。缓存不可作为管理操作授权，确认/worker仍读取实时身份。

AWS只汇总已启用且访问成功的EC2地区；无权/超时/限速地区明确失败，不从分母消失。Azure只声明当前接入资源组，不能冒充整个订阅。Linode/DO账号级分页，完整查询后才能确认零台。

价格只显示API可确认的USD小时/月广告价格。Linode优先匹配region_prices；DO使用size.price_monthly/hourly。AWS/Azure未取得可靠报价时明确未知，不显示免费。套餐磁盘、本地临时实例存储与独立EBS/Managed Disk必须区分；备份、公网IP、磁盘、流量、系统授权等额外费用提示独立显示。

正式独立审核 REQUIRED；完成实现和证据后交上游安排，未通过前不创建beta.2 Tag、不发布beta.2镜像、不合并main、不更改Owner网络配置。

状态与计费说明核对：AWS https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/ec2-instance-lifecycle.html；Azure https://learn.microsoft.com/en-us/azure/virtual-machines/states-billing；Linode https://techdocs.akamai.com/linode-api/reference/get-linode-instance；DO https://docs.digitalocean.com/products/droplets/reference/api/droplets/ 。服务器电源状态与任务状态使用独立中文标签，Azure provisioning succeeded 不冒充运行中。
