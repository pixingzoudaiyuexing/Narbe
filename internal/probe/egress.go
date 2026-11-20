package probe

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

const EgressSource = "https://api.country.is/"

// ObserveEgress observes this process' actual IPv4 route. Environment HTTP proxy
// variables are deliberately ignored; OS-level VPN/TUN still affects this route.
// Country is third-party IP geolocation, not proof of physical location.
func ObserveEgress(ctx context.Context) (Egress, error) {
	return observeEgress(ctx, "", directClient(10*time.Second))
}

// LookupEgress independently checks a supplied public IPv4 against the same
// fixed HTTPS service. It never accepts a URL or a non-public target address.
func LookupEgress(ctx context.Context, ip string) (Egress, error) {
	if !PublicIPv4(ip) {
		return Egress{}, errors.New("egress lookup requires a public IPv4")
	}
	return observeEgress(ctx, ip, directClient(10*time.Second))
}

func directClient(timeout time.Duration) *http.Client {
	d := net.Dialer{Timeout: timeout}
	return &http.Client{
		Timeout:       timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport: &http.Transport{
			Proxy: nil, DisableKeepAlives: true, MaxResponseHeaderBytes: 8 << 10,
			TLSHandshakeTimeout: timeout, ResponseHeaderTimeout: timeout,
			TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return d.DialContext(ctx, "tcp4", addr)
			},
		},
	}
}

func observeEgress(ctx context.Context, lookup string, client *http.Client) (Egress, error) {
	e := Egress{Source: EgressSource}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, EgressSource+lookup, nil)
	if err != nil {
		return e, errors.New("cannot construct egress request")
	}
	req.Header.Set("User-Agent", "Narbe-Probe/"+Version)
	resp, err := client.Do(req)
	if err != nil {
		e.Error = "无法通过已验证 HTTPS 核实 IPv4 出口"
		return e, errors.New(e.Error)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		e.Error = "出口核实服务暂不可用"
		return e, errors.New(e.Error)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4097))
	if err != nil || len(b) > 4096 {
		e.Error = "出口核实响应无效"
		return e, errors.New(e.Error)
	}
	var payload struct {
		IP      string `json:"ip"`
		Country string `json:"country"`
	}
	if json.Unmarshal(b, &payload) != nil || !PublicIPv4(payload.IP) || !ValidCountryCode(payload.Country) || lookup != "" && payload.IP != lookup {
		e.Error = "出口 IP 或地理证据无法核实"
		return e, errors.New(e.Error)
	}
	e.IP, e.Country, e.ObservedAt = payload.IP, payload.Country, time.Now().UTC()
	return e, nil
}

// ValidCountryCode excludes anonymous/unknown geolocation placeholders. Kosovo
// (XK) is included because common IP geolocation datasets use that country code.
func ValidCountryCode(code string) bool {
	const countries = " AD AE AF AG AI AL AM AO AQ AR AS AT AU AW AX AZ BA BB BD BE BF BG BH BI BJ BL BM BN BO BQ BR BS BT BV BW BY BZ CA CC CD CF CG CH CI CK CL CM CN CO CR CU CV CW CX CY CZ DE DJ DK DM DO DZ EC EE EG EH ER ES ET FI FJ FK FM FO FR GA GB GD GE GF GG GH GI GL GM GN GP GQ GR GS GT GU GW GY HK HM HN HR HT HU ID IE IL IM IN IO IQ IR IS IT JE JM JO JP KE KG KH KI KM KN KP KR KW KY KZ LA LB LC LI LK LR LS LT LU LV LY MA MC MD ME MF MG MH MK ML MM MN MO MP MQ MR MS MT MU MV MW MX MY MZ NA NC NE NF NG NI NL NO NP NR NU NZ OM PA PE PF PG PH PK PL PM PN PR PS PT PW PY QA RE RO RS RU RW SA SB SC SD SE SG SH SI SJ SK SL SM SN SO SR SS ST SV SX SY SZ TC TD TF TG TH TJ TK TL TM TN TO TR TT TV TW TZ UA UG UM US UY UZ VA VC VE VG VI VN VU WF WS XK YE YT ZA ZM ZW "
	return len(code) == 2 && strings.Contains(countries, " "+code+" ")
}

// ValidCountry is the shared manager/agent country-evidence guard.
func ValidCountry(code string) bool { return ValidCountryCode(code) }
