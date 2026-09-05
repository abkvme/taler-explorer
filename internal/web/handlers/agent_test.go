package handlers

import "testing"

// The user agent is chosen by a remote peer, so these cases include the shapes a
// peer might send by mistake as well as the ones it should.
func TestParseAgent(t *testing.T) {
	cases := []struct {
		name    string
		subver  string
		version string
		mode    string
		api     string
		reports bool
	}{
		// Before 0.20.0 nothing is reported, and the columns must stay blank
		// rather than claim the peer answered "no".
		{"old plain", "/Taler:0.19.6.8/", "0.19.6.8", "", "", false},
		{"old with comment", "/TalerCore:0.16.3.4(TalerCrypto.com)/", "0.16.3.4", "", "", false},
		{"old three part", "/Taler:0.17.2/", "0.17.2", "", "", false},
		{"just below the floor", "/Taler:0.19.99.99/", "0.19.99.99", "", "", false},

		// 0.20.0 and later.
		{"server", "/Taler:0.20.0(SERV)/", "0.20.0", "SERV", "", true},
		{"gui", "/Taler:0.20.0(GUI)/", "0.20.0", "GUI", "", true},
		{"server with api", "/Taler:0.20.0(SERV; api:1.2.0)/", "0.20.0", "SERV", "1.2.0", true},
		{"gui with api and comment", "/Taler:0.20.0(GUI; api:0.3.1; eu-1)/", "0.20.0", "GUI", "0.3.1", true},
		{"operator comment only", "/Taler:0.20.0(SERV; eu-1)/", "0.20.0", "SERV", "", true},
		{"later release", "/Taler:0.21.4(GUI)/", "0.21.4", "GUI", "", true},
		{"major bump", "/Taler:1.0.0(SERV)/", "1.0.0", "SERV", "", true},

		// A new-enough peer that sent no mode: reports, but says nothing.
		{"new but silent", "/Taler:0.20.0/", "0.20.0", "", "", true},

		// Malformed input must not be credited with features.
		{"empty", "", "", "", "", false},
		{"no version", "/Taler:/", "", "", "", false},
		{"garbage", "wat", "wat", "", "", false},
		{"unclosed comment", "/Taler:0.20.0(SERV", "0.20.0", "SERV", "", true},
		{"another implementation", "/Satoshi:0.21.0(GUI)/", "0.21.0", "GUI", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseAgent(c.subver)
			if got.Version != c.version || got.Mode != c.mode || got.API != c.api || got.Reports != c.reports {
				t.Errorf("parseAgent(%q)\n got  {%q %q %q %v}\n want {%q %q %q %v}",
					c.subver, got.Version, got.Mode, got.API, got.Reports,
					c.version, c.mode, c.api, c.reports)
			}
		})
	}
}

func TestVersionAtLeast(t *testing.T) {
	floor := [3]int{0, 20, 0}
	for _, c := range []struct {
		version string
		want    bool
	}{
		{"0.19.6.8", false}, {"0.19.99", false}, {"0.20.0", true},
		{"0.20.0.1", true}, {"0.20.1", true}, {"0.21.0", true},
		{"1.0.0", true}, {"", false}, {"x.y.z", false}, {"0.20", false},
	} {
		if got := versionAtLeast(c.version, floor); got != c.want {
			t.Errorf("versionAtLeast(%q) = %v, want %v", c.version, got, c.want)
		}
	}
}
