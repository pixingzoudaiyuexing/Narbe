package cloud

import "strings"

// Explicit display-only membership, checked against the same official region
// locations as regionNames. New or unverified IDs never inherit from a prefix.
var continentIDs = map[string]map[string]string{
	"aws": {
		"亚洲":  "ap-east-1 ap-east-2 ap-south-1 ap-south-2 ap-southeast-1 ap-southeast-3 ap-southeast-5 ap-southeast-7 ap-northeast-1 ap-northeast-2 ap-northeast-3 il-central-1 me-south-1 me-central-1",
		"欧洲":  "eu-central-1 eu-central-2 eu-west-1 eu-west-2 eu-west-3 eu-north-1 eu-south-1 eu-south-2",
		"北美洲": "us-east-1 us-east-2 us-west-1 us-west-2 ca-central-1 ca-west-1 mx-central-1",
		"南美洲": "sa-east-1", "大洋洲": "ap-southeast-2 ap-southeast-4 ap-southeast-6", "非洲": "af-south-1",
	},
	"azure": {
		"亚洲":  "eastasia southeastasia japaneast japanwest koreacentral koreasouth centralindia southindia westindia indiasouthcentral indonesiacentral malaysiawest uaenorth uaecentral qatarcentral israelcentral",
		"欧洲":  "northeurope westeurope uksouth ukwest francecentral francesouth germanynorth germanywestcentral italynorth swedencentral switzerlandnorth switzerlandwest norwayeast norwaywest polandcentral spaincentral austriaeast belgiumcentral denmarkeast",
		"北美洲": "eastus eastus2 westus westus2 westus3 centralus northcentralus southcentralus westcentralus canadacentral canadaeast mexicocentral",
		"南美洲": "brazilsouth brazilsoutheast chilecentral", "大洋洲": "australiaeast australiasoutheast australiacentral australiacentral2 newzealandnorth", "非洲": "southafricanorth southafricawest",
	},
	"linode": {
		"亚洲":  "in-maa jp-osa id-cgk in-bom-2 sg-sin-2 jp-tyo-3 ap-west ap-northeast ap-south",
		"欧洲":  "gb-lon eu-west se-sto es-mad it-mil nl-ams de-fra-2 fr-par-2 fr-par eu-central",
		"北美洲": "us-mia us-lax us-iad-2 us-sea us-ord us-iad ca-central us-central us-east us-southeast us-west",
		"南美洲": "br-gru", "大洋洲": "au-mel ap-southeast",
	},
	"digitalocean": {
		"亚洲": "sgp1 blr1", "欧洲": "ams2 ams3 lon1 fra1", "北美洲": "nyc1 nyc2 nyc3 sfo1 sfo2 sfo3 tor1 atl1 ric1 mkc1 mem1", "大洋洲": "syd1",
	},
}

func RegionContinent(provider, id string) string {
	for continent, ids := range continentIDs[provider] {
		for _, known := range strings.Fields(ids) {
			if known == id {
				return continent
			}
		}
	}
	return "其他／位置待确认"
}
