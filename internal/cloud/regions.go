package cloud

// Display-only names checked against the official sources in docs/BETA2_SOURCES.md.
// IDs are never translated, inferred from prefixes, or used to invent geography.
var regionNames = map[string]map[string]string{
	"aws": {
		"us-east-1": "美国东部（弗吉尼亚北部）", "us-east-2": "美国东部（俄亥俄）", "us-west-1": "美国西部（加利福尼亚北部）", "us-west-2": "美国西部（俄勒冈）",
		"af-south-1": "南非开普敦", "ap-east-1": "中国香港", "ap-east-2": "中国台北", "ap-south-1": "印度孟买", "ap-south-2": "印度海得拉巴",
		"ap-southeast-1": "新加坡", "ap-southeast-2": "澳大利亚悉尼", "ap-southeast-3": "印度尼西亚雅加达", "ap-southeast-4": "澳大利亚墨尔本", "ap-southeast-5": "马来西亚", "ap-southeast-6": "新西兰", "ap-southeast-7": "泰国",
		"ap-northeast-1": "日本东京", "ap-northeast-2": "韩国首尔", "ap-northeast-3": "日本大阪", "ca-central-1": "加拿大中部", "ca-west-1": "加拿大卡尔加里",
		"eu-central-1": "德国法兰克福", "eu-central-2": "瑞士苏黎世", "eu-west-1": "爱尔兰", "eu-west-2": "英国伦敦", "eu-west-3": "法国巴黎", "eu-north-1": "瑞典斯德哥尔摩", "eu-south-1": "意大利米兰", "eu-south-2": "西班牙",
		"il-central-1": "以色列特拉维夫", "mx-central-1": "墨西哥中部", "me-south-1": "巴林", "me-central-1": "阿拉伯联合酋长国", "sa-east-1": "巴西圣保罗",
	},
	"azure": {
		"eastus": "美国弗吉尼亚", "eastus2": "美国弗吉尼亚", "westus": "美国加利福尼亚", "westus2": "美国华盛顿州", "westus3": "美国菲尼克斯", "centralus": "美国艾奥瓦", "northcentralus": "美国伊利诺伊", "southcentralus": "美国得克萨斯", "westcentralus": "美国怀俄明",
		"canadacentral": "加拿大多伦多", "canadaeast": "加拿大魁北克", "brazilsouth": "巴西圣保罗州", "brazilsoutheast": "巴西里约热内卢", "chilecentral": "智利圣地亚哥", "mexicocentral": "墨西哥克雷塔罗州",
		"eastasia": "中国香港", "southeastasia": "新加坡", "japaneast": "日本东京／埼玉", "japanwest": "日本大阪", "koreacentral": "韩国首尔", "koreasouth": "韩国釜山", "centralindia": "印度浦那", "southindia": "印度金奈", "westindia": "印度孟买", "indiasouthcentral": "印度海得拉巴", "indonesiacentral": "印度尼西亚雅加达", "malaysiawest": "马来西亚吉隆坡",
		"australiaeast": "澳大利亚新南威尔士", "australiasoutheast": "澳大利亚维多利亚", "australiacentral": "澳大利亚堪培拉", "australiacentral2": "澳大利亚堪培拉", "newzealandnorth": "新西兰奥克兰",
		"northeurope": "爱尔兰", "westeurope": "荷兰", "uksouth": "英国伦敦", "ukwest": "英国加的夫", "francecentral": "法国巴黎", "francesouth": "法国马赛", "germanynorth": "德国柏林", "germanywestcentral": "德国法兰克福", "italynorth": "意大利米兰", "swedencentral": "瑞典耶夫勒", "switzerlandnorth": "瑞士苏黎世", "switzerlandwest": "瑞士日内瓦", "norwayeast": "挪威", "norwaywest": "挪威", "polandcentral": "波兰华沙", "spaincentral": "西班牙马德里", "austriaeast": "奥地利维也纳", "belgiumcentral": "比利时布鲁塞尔", "denmarkeast": "丹麦哥本哈根",
		"uaenorth": "阿联酋迪拜", "uaecentral": "阿联酋阿布扎比", "qatarcentral": "卡塔尔多哈", "israelcentral": "以色列", "southafricanorth": "南非约翰内斯堡", "southafricawest": "南非开普敦",
	},
	"linode": {
		"gb-lon": "英国伦敦 2", "eu-west": "英国伦敦", "se-sto": "瑞典斯德哥尔摩", "es-mad": "西班牙马德里", "in-maa": "印度金奈", "jp-osa": "日本大阪", "it-mil": "意大利米兰", "us-mia": "美国迈阿密", "id-cgk": "印度尼西亚雅加达", "us-lax": "美国洛杉矶", "nl-ams": "荷兰阿姆斯特丹", "au-mel": "澳大利亚墨尔本", "in-bom-2": "印度孟买 2", "de-fra-2": "德国法兰克福 2", "sg-sin-2": "新加坡 2", "jp-tyo-3": "日本东京 3", "us-iad-2": "美国华盛顿 2", "fr-par-2": "法国巴黎 2", "br-gru": "巴西圣保罗", "us-sea": "美国西雅图", "fr-par": "法国巴黎", "us-ord": "美国芝加哥", "us-iad": "美国华盛顿", "ap-southeast": "澳大利亚悉尼", "ca-central": "加拿大多伦多", "ap-west": "印度孟买", "us-central": "美国达拉斯", "ap-northeast": "日本东京 2", "eu-central": "德国法兰克福", "ap-south": "新加坡", "us-east": "美国纽瓦克", "us-southeast": "美国亚特兰大", "us-west": "美国弗里蒙特",
	},
	"digitalocean": {
		"nyc1": "美国纽约 1", "nyc2": "美国纽约 2", "nyc3": "美国纽约 3", "ams2": "荷兰阿姆斯特丹 2", "ams3": "荷兰阿姆斯特丹 3", "sfo1": "美国旧金山 1", "sfo2": "美国旧金山 2", "sfo3": "美国旧金山 3", "sgp1": "新加坡", "lon1": "英国伦敦", "fra1": "德国法兰克福", "tor1": "加拿大多伦多", "blr1": "印度班加罗尔", "syd1": "澳大利亚悉尼", "atl1": "美国亚特兰大", "ric1": "美国里士满", "mkc1": "美国堪萨斯城", "mem1": "美国孟菲斯",
	},
}

func RegionLabel(provider, id, official string) string {
	if name := regionNames[provider][id]; name != "" {
		return name + " · " + id
	}
	if official != "" && official != id {
		return official + " · " + id
	}
	return id + "（位置名称未确认）"
}

func LocalizeRegions(provider string, regions []Choice) {
	for i := range regions {
		regions[i].Name = RegionLabel(provider, regions[i].ID, regions[i].Name)
	}
}
