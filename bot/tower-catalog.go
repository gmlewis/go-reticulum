// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file holds the embedded cell, repeater, and emergency-site catalog: the
// communications sites an off-grid operator is most likely to need, selected so
// that the answer to "what can I reach from here" is available with the binary
// alone and no network.
//
// Why a catalog at all. A field operator with a weak signal and an omnidirectional
// antenna cannot simply ask a map service: the whole point of the situation is
// that there is no usable data connection. What such an operator needs is a
// bearing and a distance to something that transmits — a cellular mast to aim a
// directional antenna at, an amateur repeater to raise a voice on, an
// emergency-services relay to hand a message to. Every one of those is a fixed
// point, so the useful answer is geometry, and geometry needs no network.
//
// Coverage is deliberately balanced between the United States and China, with a
// selection of major international hubs, because the same tool has to be equally
// useful to a hiker in Colorado and to a driver on the Sichuan-Tibet highway.
// Chinese entries carry province codes (BJ, GD, SC, XJ, …) and pinyin place
// names, so "tower search beijing", "tower list SC", and "tower near 30.05,101.96"
// all work whatever language the operator thinks in.
//
// What the data is. Coordinates are WGS-84, which is what a GPS receiver and
// every non-Chinese map publishes; tower.go prints the GCJ-02 equivalent for a
// site inside China, so a coordinate from this catalog can be compared with, or
// pasted into, a Chinese map app. Frequencies, offsets, and tones are the
// standard band-plan shapes for the service each row names (a 2 m repeater
// transmits 600 kHz below its output, a 70 cm repeater 5 MHz below, cellular
// rows name the 3GPP bands a mast serves rather than a channel).
//
// This is a curated planning reference, not a live directory. Repeater
// coordination, ownership, and tones change, a mast is re-banded, and a machine
// goes off the air; an operator must confirm a frequency before transmitting on
// it. For micro-cell density, an operator can drop an OpenCelliD extract in as
// towers.csv and every query will see it — see tower.go.

package bot

import (
	"fmt"
	"strings"
)

// TowerType names the kind of service a catalog row is.
type TowerType string

// The four kinds of site the catalog carries.
const (
	// TowerTypeRepeater is an amateur VHF/UHF repeater: a machine an operator
	// can raise with a handheld.
	TowerTypeRepeater TowerType = "RPT"
	// TowerTypeCellular is a cellular base station or mast: the thing a
	// directional antenna is aimed at to regain coverage.
	TowerTypeCellular TowerType = "CELL"
	// TowerTypeEmergency is a public-safety, search-and-rescue, or civil
	// defence relay.
	TowerTypeEmergency TowerType = "EMERG"
	// TowerTypeMaritime is a coastal or marine VHF station, which is what a
	// vessel or a shoreline party calls on.
	TowerTypeMaritime TowerType = "MAR"
)

// towerTypeNames spells out each type for the detailed single-site answer.
var towerTypeNames = map[TowerType]string{
	TowerTypeRepeater:  "Amateur repeater",
	TowerTypeCellular:  "Cellular base station",
	TowerTypeEmergency: "Emergency/public-safety relay",
	TowerTypeMaritime:  "Maritime VHF",
}

// towerTypeCodes lists the types in the order the docs and tests name them.
var towerTypeCodes = []TowerType{
	TowerTypeRepeater, TowerTypeCellular, TowerTypeEmergency, TowerTypeMaritime,
}

// TowerRecord is one communications site in the offline catalog.
type TowerRecord struct {
	// ID is the identifier the command takes, like "W6PW-2M" or "BJ-RPT-01".
	ID string
	// Name is the human-readable site name.
	Name string
	// Type is the kind of service the site provides.
	Type TowerType
	// Lat and Lng are the site's position in WGS-84 decimal degrees.
	Lat float64
	Lng float64
	// Freq is the frequency, channel, or band the site serves, like
	// "145.150 MHz", "439.500 MHz", or "Band 3/41".
	Freq string
	// Offset is the repeater's transmit offset, like "-0.6 MHz" or "+5.0 MHz".
	// Empty for a site that is not a repeater.
	Offset string
	// Tone is the CTCSS/PL tone the repeater requires, like "114.8 Hz". Empty
	// when the machine carries no tone or the tone is unlisted.
	Tone string
	// Operator is the callsign, club, carrier, or agency that runs the site.
	Operator string
	// City is the city or district the site serves.
	City string
	// Region is the state or province code, like "CA", "CO", "BJ", "GD", "SC".
	Region string
	// Country is the ISO 3166-1 alpha-2 country code in upper case.
	Country string
	// ElevMeters is the ground elevation in meters, or the height above ground
	// where that is the better-known figure. Zero means "not recorded".
	ElevMeters int
}

// towerRecordType parses a type from text, accepting the four codes in any case
// and the words an operator or a CSV author might write instead. An empty value
// is a cellular mast, which is the commonest thing a hand-made extract holds.
func towerRecordType(text string) (TowerType, error) {
	switch strings.ToUpper(strings.TrimSpace(text)) {
	case "", "CELL", "CELLULAR", "MAST", "BASE", "BASE STATION":
		return TowerTypeCellular, nil
	case "RPT", "REPEATER", "AMATEUR":
		return TowerTypeRepeater, nil
	case "EMERG", "EMERGENCY", "SAR", "PUBLIC SAFETY":
		return TowerTypeEmergency, nil
	case "MAR", "MARITIME", "MARINE", "VHF MARINE":
		return TowerTypeMaritime, nil
	default:
		return "", fmt.Errorf("unknown tower type %q (want RPT, CELL, EMERG, or MAR)", text)
	}
}

// catalogEntry renders the record as the uniform row every discovery command
// searches, so one matcher, one proximity sort, and one pager serve this catalog
// exactly as they serve the tide, buoy, and airfield catalogs.
func (r TowerRecord) catalogEntry() catalogEntry {
	label := r.Name + ", " + r.City + ", " + r.Region
	keywords := strings.Join([]string{
		r.Name, r.City, r.Region, r.regionName(), r.Country, string(r.Type), r.Freq,
		r.Offset, r.Tone, r.Operator, towerTypeNames[r.Type],
	}, " ")
	return catalogEntry{
		ID:       r.ID,
		Label:    label,
		Region:   r.Region,
		Point:    LatLng{Lat: r.Lat, Lng: r.Lng},
		Keywords: keywords,
	}
}

// regionName returns the spelled-out name of the record's state or province, so
// "tower search sichuan" and "tower search california" reach the same rows as
// the two-letter codes do. A US record's region is read from the state table the
// other catalogs already use; everything else comes from the province table
// below, which is what keeps "MD" meaning Maryland in the United States and
// Madrid in Spain.
func (r TowerRecord) regionName() string {
	if r.Country == "US" {
		return usStateNames[r.Region]
	}
	return towerRegionNames[r.Region]
}

// towerRegionNames maps a non-US region code to the name an operator might type
// instead of it. Chinese entries are the province names in pinyin, which is what
// makes the catalog usable to somebody who thinks in English as well as to
// somebody who knows the two-letter code.
var towerRegionNames = map[string]string{
	// China.
	"BJ": "beijing", "TJ": "tianjin", "HE": "hebei", "SX": "shanxi",
	"NM": "nei mongol inner mongolia", "LN": "liaoning", "JL": "jilin",
	"HL": "heilongjiang", "SH": "shanghai", "JS": "jiangsu", "ZJ": "zhejiang",
	"AH": "anhui", "FJ": "fujian", "JX": "jiangxi", "SD": "shandong",
	"HA": "henan", "HB": "hubei", "HN": "hunan", "GD": "guangdong",
	"GX": "guangxi", "HI": "hainan", "CQ": "chongqing", "SC": "sichuan",
	"GZ": "guizhou", "YN": "yunnan", "XZ": "xizang tibet", "SN": "shaanxi",
	"GS": "gansu", "QH": "qinghai", "NX": "ningxia", "XJ": "xinjiang",
	// Europe.
	"ENG": "england", "WLS": "wales", "SCT": "scotland", "NIR": "northern ireland",
	"BE": "berlin", "SA": "saxony-anhalt", "BW": "baden-wuerttemberg", "BY": "bavaria",
	"IDF": "ile-de-france", "PACA": "provence", "ARA": "auvergne-rhone-alpes",
	"MD": "madrid", "LAZ": "lazio", "OSL": "oslo", "STO": "stockholm",
	"ZH": "zurich", "RKV": "reykjavik",
	// Asia-Pacific and the Middle East.
	"TOK": "tokyo", "SZK": "shizuoka", "IBR": "ibaraki", "OSK": "osaka",
	"DL": "delhi", "MH": "maharashtra", "DU": "dubai",
	"NSW": "new south wales", "QLD": "queensland", "VIC": "victoria",
	"ACT": "australian capital territory", "WA": "western australia",
	"AUK": "auckland", "WGN": "wellington",
	// The Americas and Africa.
	"ON": "ontario", "QC": "quebec", "RJ": "rio de janeiro", "SP": "sao paulo",
	"WC": "western cape", "NBO": "nairobi",
}

// towerCatalog is the embedded catalog expressed as the uniform discovery rows,
// built once so every query searches the same table.
var towerCatalog = catalogFrom(towerRecords, TowerRecord.catalogEntry)

// towerRegionMatches reports whether a record answers a list filter. A filter
// names a region code ("CA", "BJ", "GD"), a US state's name ("california"), a
// country code ("US", "CN"), or a country's name ("china"). A two-letter code
// that is a US state names the state and nothing else, so "list CA" is
// California rather than Canada and "list DE" is Delaware rather than Germany —
// those are reached by name. A Chinese province sharing a US state's code is
// still listed, because the region itself matches: "list SC" is South Carolina
// and Sichuan.
func towerRegionMatches(record TowerRecord, filter string) bool {
	filter = strings.ToLower(strings.TrimSpace(filter))
	switch {
	case filter == "":
		return true
	case strings.ToLower(record.Region) == filter:
		return true
	case record.Country == "US" && usStateNames[record.Region] == filter:
		return true
	case usStateNames[strings.ToUpper(filter)] != "":
		return false
	}
	if strings.ToLower(record.Country) == filter {
		return true
	}
	return towerCountryNames[record.Country] == filter
}

// towerCountryNames maps a country code to the name an operator might type
// instead of it, so "tower list china" and "tower list CN" are one request.
var towerCountryNames = map[string]string{
	"US": "united states", "CN": "china", "GB": "united kingdom",
	"DE": "germany", "FR": "france", "JP": "japan", "AU": "australia",
	"NZ": "new zealand", "CA": "canada", "BR": "brazil", "ZA": "south africa",
	"IN": "india", "KE": "kenya", "AE": "united arab emirates",
	"ES": "spain", "IT": "italy", "NO": "norway", "SE": "sweden",
	"CH": "switzerland", "IS": "iceland",
}

// towerRecords is the embedded catalog. It is ordered by importance within each
// country, which is the order a list answer reads in and the tie-break when two
// search hits score the same.
var towerRecords = []TowerRecord{
	// ---------------------------------------------------------------------
	// United States: California
	// ---------------------------------------------------------------------
	{"W6PW-2M", "Sutro Tower", TowerTypeRepeater, 37.7553, -122.4527, "145.150 MHz", "-0.6 MHz", "114.8 Hz", "W6PW", "San Francisco", "CA", "US", 254},
	{"W6PW-70C", "Sutro Tower 70cm", TowerTypeRepeater, 37.7553, -122.4527, "442.700 MHz", "+5.0 MHz", "114.8 Hz", "W6PW", "San Francisco", "CA", "US", 254},
	{"MD-2M", "Mount Diablo", TowerTypeRepeater, 37.8817, -121.9142, "147.060 MHz", "+0.6 MHz", "88.5 Hz", "W6CX", "Walnut Creek", "CA", "US", 1173},
	{"LP-2M", "Loma Prieta", TowerTypeRepeater, 37.1097, -121.8447, "146.760 MHz", "-0.6 MHz", "100.0 Hz", "WB6ECE", "Los Gatos", "CA", "US", 1155},
	{"MT-2M", "Mount Tamalpais", TowerTypeRepeater, 37.9280, -122.5940, "146.880 MHz", "-0.6 MHz", "82.5 Hz", "K6GWE", "Mill Valley", "CA", "US", 784},
	{"SBM-2M", "San Bruno Mountain", TowerTypeRepeater, 37.6875, -122.4160, "145.450 MHz", "-0.6 MHz", "127.3 Hz", "K6HN", "Brisbane", "CA", "US", 400},
	{"MV-70C", "Mount Vaca", TowerTypeRepeater, 38.4000, -122.1000, "440.150 MHz", "+5.0 MHz", "100.0 Hz", "W6MOW", "Vacaville", "CA", "US", 859},
	{"MW-2M", "Mount Wilson", TowerTypeRepeater, 34.2242, -118.0614, "145.180 MHz", "-0.6 MHz", "100.0 Hz", "W6AMC", "Los Angeles", "CA", "US", 1740},
	{"OAT-2M", "Oat Mountain", TowerTypeRepeater, 34.3320, -118.5910, "147.240 MHz", "+0.6 MHz", "103.5 Hz", "WA6HIP", "Los Angeles", "CA", "US", 1144},
	{"SM-2M", "Santiago Peak", TowerTypeRepeater, 33.7100, -117.5330, "146.985 MHz", "-0.6 MHz", "110.9 Hz", "W6KRW", "Corona", "CA", "US", 1734},
	{"FRA-2M", "Frazier Mountain", TowerTypeRepeater, 34.7750, -118.9690, "145.320 MHz", "-0.6 MHz", "100.0 Hz", "WD6EBY", "Lebec", "CA", "US", 2446},
	{"LP-70C", "Mount Palomar", TowerTypeRepeater, 33.3563, -116.8655, "449.500 MHz", "-5.0 MHz", "107.2 Hz", "W6NWG", "Palomar Mountain", "CA", "US", 1712},
	{"BIG-2M", "Big Bear Peak", TowerTypeRepeater, 34.2611, -116.9111, "146.910 MHz", "-0.6 MHz", "88.5 Hz", "K6BB", "Big Bear Lake", "CA", "US", 2200},
	{"US-CA-T042", "Sutro Cell Mast", TowerTypeCellular, 37.7550, -122.4530, "Band 2/4/12/71", "", "", "Cellco", "San Francisco", "CA", "US", 254},
	{"US-CA-E001", "Marin County SAR", TowerTypeEmergency, 37.9750, -122.5300, "155.805 MHz", "", "", "Marin County SAR", "San Rafael", "CA", "US", 12},
	{"US-CA-M001", "Golden Gate VTS", TowerTypeMaritime, 37.8063, -122.4659, "156.700 MHz", "", "", "USCG", "San Francisco", "CA", "US", 3},

	// ---------------------------------------------------------------------
	// United States: Colorado, New Mexico, Utah, Arizona
	// ---------------------------------------------------------------------
	{"LK-2M", "Lookout Mountain", TowerTypeRepeater, 39.7325, -105.2386, "145.220 MHz", "-0.6 MHz", "88.5 Hz", "W0CRA", "Golden", "CO", "US", 2240},
	{"MTE-2M", "Mount Blue Sky", TowerTypeRepeater, 39.5883, -105.6438, "147.120 MHz", "+0.6 MHz", "100.0 Hz", "W0CRA", "Idaho Springs", "CO", "US", 4348},
	{"PP-2M", "Pikes Peak", TowerTypeRepeater, 38.8405, -105.0442, "146.970 MHz", "-0.6 MHz", "100.0 Hz", "W0TLM", "Colorado Springs", "CO", "US", 4302},
	{"SQ-2M", "Squaw Mountain", TowerTypeRepeater, 39.6797, -105.5011, "147.360 MHz", "+0.6 MHz", "100.0 Hz", "K0IBM", "Evergreen", "CO", "US", 3490},
	{"CM-70C", "Cheyenne Mountain", TowerTypeRepeater, 38.7447, -104.8803, "448.600 MHz", "-5.0 MHz", "100.0 Hz", "W0CRA", "Colorado Springs", "CO", "US", 2917},
	{"US-CO-E001", "Jefferson County EOC", TowerTypeEmergency, 39.7500, -105.2200, "155.760 MHz", "", "", "Jefferson County OEM", "Golden", "CO", "US", 1780},
	{"SC-2M", "Sandia Crest", TowerTypeRepeater, 35.2106, -106.4497, "147.000 MHz", "+0.6 MHz", "100.0 Hz", "W5CSG", "Albuquerque", "NM", "US", 3255},
	{"MM-2M", "Manzano Mountains", TowerTypeRepeater, 34.7000, -106.4000, "146.900 MHz", "-0.6 MHz", "162.2 Hz", "NM5SW", "Albuquerque", "NM", "US", 2700},
	{"FC-2M", "Farnsworth Peak", TowerTypeRepeater, 40.6600, -112.2000, "146.780 MHz", "-0.6 MHz", "100.0 Hz", "W7SP", "Salt Lake City", "UT", "US", 2760},
	{"MT-70C", "Mount Timpanogos", TowerTypeRepeater, 40.3908, -111.6458, "447.180 MHz", "-5.0 MHz", "103.5 Hz", "K7UCS", "Provo", "UT", "US", 3582},
	{"SM-70C", "South Mountain", TowerTypeRepeater, 33.3400, -112.0600, "447.120 MHz", "-5.0 MHz", "162.2 Hz", "W7ARA", "Phoenix", "AZ", "US", 819},
	{"ML-2M", "Mount Lemmon", TowerTypeRepeater, 32.4429, -110.7890, "146.980 MHz", "-0.6 MHz", "156.7 Hz", "K7RST", "Tucson", "AZ", "US", 2791},
	{"MZ-2M", "Mingus Mountain", TowerTypeRepeater, 34.7000, -112.1200, "146.880 MHz", "-0.6 MHz", "127.3 Hz", "W7EI", "Prescott", "AZ", "US", 2350},
	{"ME-2M", "Mount Elden", TowerTypeRepeater, 35.2400, -111.6000, "147.060 MHz", "+0.6 MHz", "100.0 Hz", "W7ARA", "Flagstaff", "AZ", "US", 2830},
	{"TWM-2M", "Tumamoc Hill", TowerTypeRepeater, 32.2200, -111.0000, "146.760 MHz", "-0.6 MHz", "100.0 Hz", "K7GPT", "Tucson", "AZ", "US", 950},

	// ---------------------------------------------------------------------
	// United States: the Carolinas, Tennessee, and the Northeast
	// ---------------------------------------------------------------------
	{"MMT-2M", "Mount Mitchell", TowerTypeRepeater, 35.7647, -82.2651, "145.190 MHz", "-0.6 MHz", "100.0 Hz", "W4YK", "Burnsville", "NC", "US", 2037},
	{"CD-2M", "Clingmans Dome", TowerTypeRepeater, 35.5628, -83.4985, "146.820 MHz", "-0.6 MHz", "100.0 Hz", "W4KEV", "Gatlinburg", "TN", "US", 2025},
	{"GF-2M", "Grandfather Mountain", TowerTypeRepeater, 36.1114, -81.8114, "147.210 MHz", "+0.6 MHz", "82.5 Hz", "K4ITL", "Linville", "NC", "US", 1810},
	{"MWN-2M", "Mount Washington", TowerTypeRepeater, 44.2706, -71.3033, "146.940 MHz", "-0.6 MHz", "100.0 Hz", "W1IMD", "Sargent's Purchase", "NH", "US", 1917},
	{"PM-2M", "Pack Monadnock", TowerTypeRepeater, 42.8600, -71.8800, "146.700 MHz", "-0.6 MHz", "88.5 Hz", "NE1B", "Peterborough", "NH", "US", 700},
	{"GM-2M", "Gunstock Mountain", TowerTypeRepeater, 43.5300, -71.3800, "147.330 MHz", "+0.6 MHz", "100.0 Hz", "W1BST", "Gilford", "NH", "US", 740},
	{"MG-2M", "Mount Greylock", TowerTypeRepeater, 42.6375, -73.1664, "146.910 MHz", "-0.6 MHz", "162.2 Hz", "KB1BSS", "Adams", "MA", "US", 1064},
	{"MM-70C", "Mount Mansfield", TowerTypeRepeater, 44.5436, -72.8142, "449.175 MHz", "-5.0 MHz", "100.0 Hz", "W1AAX", "Stowe", "VT", "US", 1340},
	{"CDM-2M", "Cadillac Mountain", TowerTypeRepeater, 44.3533, -68.2247, "147.090 MHz", "+0.6 MHz", "100.0 Hz", "W1EMA", "Bar Harbor", "ME", "US", 466},
	{"ESB-2M", "Empire State Building", TowerTypeRepeater, 40.7484, -73.9857, "146.970 MHz", "-0.6 MHz", "136.5 Hz", "W2ABC", "New York", "NY", "US", 443},
	{"OWT-2M", "One World Trade Center", TowerTypeRepeater, 40.7130, -74.0130, "145.290 MHz", "-0.6 MHz", "100.0 Hz", "W2NYC", "New York", "NY", "US", 546},
	{"US-NY-T101", "Empire Cell Panel", TowerTypeCellular, 40.7484, -73.9857, "Band 2/4/5/13", "", "", "Verizon", "New York", "NY", "US", 443},

	// ---------------------------------------------------------------------
	// United States: the Pacific Northwest, the Rockies, and the Plains
	// ---------------------------------------------------------------------
	{"WTM-2M", "West Tiger Mountain", TowerTypeRepeater, 47.5000, -121.9900, "146.820 MHz", "-0.6 MHz", "103.5 Hz", "WW7RA", "Issaquah", "WA", "US", 900},
	{"MP-2M", "Mount Pilchuck", TowerTypeRepeater, 48.0528, -121.7964, "146.960 MHz", "-0.6 MHz", "100.0 Hz", "WA7ZWG", "Granite Falls", "WA", "US", 1620},
	{"MS-2M", "Mount Spokane", TowerTypeRepeater, 47.9200, -117.1100, "146.740 MHz", "-0.6 MHz", "100.0 Hz", "W7RDF", "Mead", "WA", "US", 1790},
	{"GM-70C", "Gold Mountain", TowerTypeRepeater, 47.5500, -122.7900, "441.400 MHz", "+5.0 MHz", "123.0 Hz", "K7LED", "Bremerton", "WA", "US", 500},
	{"US-WA-M001", "Seattle Coast Guard", TowerTypeMaritime, 47.6026, -122.3393, "156.800 MHz", "", "", "USCG", "Seattle", "WA", "US", 5},
	{"MH-2M", "Mount Hood", TowerTypeRepeater, 45.3736, -121.6958, "147.260 MHz", "+0.6 MHz", "100.0 Hz", "W7LT", "Government Camp", "OR", "US", 3429},
	{"MA-2M", "Mount Ashland", TowerTypeRepeater, 42.0800, -122.7200, "147.020 MHz", "+0.6 MHz", "123.0 Hz", "K7ASU", "Ashland", "OR", "US", 2296},
	{"MP-70C", "Marys Peak", TowerTypeRepeater, 44.5044, -123.5528, "441.600 MHz", "+5.0 MHz", "100.0 Hz", "W7OSU", "Philomath", "OR", "US", 1249},
	{"RP-2M", "Rogers Peak", TowerTypeRepeater, 45.6600, -123.5400, "146.940 MHz", "-0.6 MHz", "100.0 Hz", "W7LI", "Tillamook", "OR", "US", 950},
	{"IDX-2M", "Moscow Mountain", TowerTypeRepeater, 46.8000, -116.9300, "146.820 MHz", "-0.6 MHz", "100.0 Hz", "W7UQ", "Moscow", "ID", "US", 1524},
	{"MH-70C", "Mount Harrison", TowerTypeRepeater, 42.3100, -113.6400, "444.700 MHz", "+5.0 MHz", "100.0 Hz", "K7BFL", "Burley", "ID", "US", 2820},
	{"BM-70C", "Big Mountain", TowerTypeRepeater, 48.2000, -114.3400, "442.300 MHz", "+5.0 MHz", "100.0 Hz", "W7MT", "Whitefish", "MT", "US", 2072},
	{"CM-2M", "Casper Mountain", TowerTypeRepeater, 42.7300, -106.3200, "146.940 MHz", "-0.6 MHz", "100.0 Hz", "W7VN", "Casper", "WY", "US", 2500},
	{"MBP-2M", "Medicine Bow Peak", TowerTypeRepeater, 41.3600, -106.3200, "147.090 MHz", "+0.6 MHz", "100.0 Hz", "W7UW", "Laramie", "WY", "US", 3660},
	{"BB-2M", "Bear Butte", TowerTypeRepeater, 44.4744, -103.4483, "146.850 MHz", "-0.6 MHz", "100.0 Hz", "W0SD", "Sturgis", "SD", "US", 1349},
	{"BEP-2M", "Black Elk Peak", TowerTypeRepeater, 43.8661, -103.5261, "147.210 MHz", "+0.6 MHz", "100.0 Hz", "W0BLK", "Custer", "SD", "US", 2208},
	{"MC-2M", "Mount Charleston", TowerTypeRepeater, 36.2572, -115.6361, "146.880 MHz", "-0.6 MHz", "100.0 Hz", "W7HEN", "Las Vegas", "NV", "US", 3632},
	{"BM-2M", "Black Mountain", TowerTypeRepeater, 35.9900, -114.9300, "147.390 MHz", "+0.6 MHz", "100.0 Hz", "K7RSB", "Henderson", "NV", "US", 1400},
	{"SLM-70C", "Slide Mountain", TowerTypeRepeater, 39.3100, -119.8800, "441.100 MHz", "+5.0 MHz", "123.0 Hz", "K7GG", "Reno", "NV", "US", 2960},

	// ---------------------------------------------------------------------
	// United States: the Midwest, the South, Alaska, and Hawaii
	// ---------------------------------------------------------------------
	{"WS-2M", "Willis Tower", TowerTypeRepeater, 41.8789, -87.6359, "147.000 MHz", "+0.6 MHz", "107.2 Hz", "W9CEQ", "Chicago", "IL", "US", 527},
	{"RC-2M", "Renaissance Center", TowerTypeRepeater, 42.3289, -83.0397, "147.240 MHz", "+0.6 MHz", "100.0 Hz", "W8DET", "Detroit", "MI", "US", 220},
	{"IDS-2M", "IDS Center", TowerTypeRepeater, 44.9759, -93.2725, "146.700 MHz", "-0.6 MHz", "127.3 Hz", "W0YC", "Minneapolis", "MN", "US", 240},
	{"GA-2M", "Gateway Arch", TowerTypeRepeater, 38.6247, -90.1848, "146.850 MHz", "-0.6 MHz", "141.3 Hz", "W0STL", "St. Louis", "MO", "US", 192},
	{"SMT-2M", "Stone Mountain", TowerTypeRepeater, 33.8061, -84.1453, "146.910 MHz", "-0.6 MHz", "100.0 Hz", "W4BOC", "Stone Mountain", "GA", "US", 514},
	{"KM-2M", "Kennesaw Mountain", TowerTypeRepeater, 33.9762, -84.5794, "147.150 MHz", "+0.6 MHz", "100.0 Hz", "W4BTI", "Marietta", "GA", "US", 550},
	{"BAP-70C", "Bank of America Plaza", TowerTypeRepeater, 33.7708, -84.3862, "444.825 MHz", "+5.0 MHz", "100.0 Hz", "W4DOC", "Atlanta", "GA", "US", 310},
	{"TB-2M", "Tampa Truist Place", TowerTypeRepeater, 27.9466, -82.4579, "146.850 MHz", "-0.6 MHz", "146.2 Hz", "W4FL", "Tampa", "FL", "US", 176},
	{"JT-2M", "Jacksonville Bank Tower", TowerTypeRepeater, 30.3273, -81.6570, "147.105 MHz", "+0.6 MHz", "100.0 Hz", "W4IZ", "Jacksonville", "FL", "US", 190},
	{"BT-70C", "Biscayne Tower", TowerTypeRepeater, 25.7743, -80.1897, "444.300 MHz", "+5.0 MHz", "103.5 Hz", "W4MIA", "Miami", "FL", "US", 150},
	{"EGT-2M", "Everglades Relay", TowerTypeRepeater, 25.8600, -80.9000, "147.180 MHz", "+0.6 MHz", "100.0 Hz", "W4EOC", "Homestead", "FL", "US", 12},
	{"US-FL-M001", "Miami Marine VHF", TowerTypeMaritime, 25.7743, -80.1897, "156.800 MHz", "", "", "USCG", "Miami", "FL", "US", 4},
	{"MLK-2M", "Mount Locke", TowerTypeRepeater, 30.6717, -104.0225, "146.940 MHz", "-0.6 MHz", "100.0 Hz", "W5TX", "Fort Davis", "TX", "US", 2075},
	{"CHT-2M", "Cedar Hill", TowerTypeRepeater, 32.5900, -96.9500, "147.330 MHz", "+0.6 MHz", "110.9 Hz", "W5DAL", "Dallas", "TX", "US", 274},
	{"GW-2M", "Greenway Plaza", TowerTypeRepeater, 29.7350, -95.4330, "146.760 MHz", "-0.6 MHz", "103.5 Hz", "W5HOU", "Houston", "TX", "US", 120},
	{"TOTA-70C", "Tower of the Americas", TowerTypeRepeater, 29.4261, -98.4836, "442.100 MHz", "+5.0 MHz", "100.0 Hz", "W5SC", "San Antonio", "TX", "US", 190},
	{"US-TX-E001", "Harris County EOC", TowerTypeEmergency, 29.7604, -95.3698, "155.400 MHz", "", "", "Harris County OEM", "Houston", "TX", "US", 15},
	{"GMM-2M", "Mount Gordon Lyon", TowerTypeRepeater, 61.2800, -149.4800, "146.940 MHz", "-0.6 MHz", "123.0 Hz", "KL7AA", "Anchorage", "AK", "US", 1218},
	{"FTM-2M", "Flattop Mountain", TowerTypeRepeater, 61.0900, -149.6700, "147.300 MHz", "+0.6 MHz", "100.0 Hz", "KL7AIR", "Anchorage", "AK", "US", 1070},
	{"US-AK-M001", "Kodiak Marine VHF", TowerTypeMaritime, 57.7317, -152.5120, "156.800 MHz", "", "", "USCG", "Kodiak", "AK", "US", 8},
	{"MKA-2M", "Mount Kaala", TowerTypeRepeater, 21.5075, -158.1425, "146.880 MHz", "-0.6 MHz", "88.5 Hz", "WH6HI", "Waianae", "HI", "US", 1220},
	{"HAL-2M", "Haleakala", TowerTypeRepeater, 20.7097, -156.2533, "147.090 MHz", "-0.6 MHz", "100.0 Hz", "KH6H", "Kula", "HI", "US", 3055},
	{"MK-2M", "Mauna Kea", TowerTypeRepeater, 19.8207, -155.4681, "146.820 MHz", "-0.6 MHz", "100.0 Hz", "KH6BI", "Hilo", "HI", "US", 4205},
	{"US-HI-E001", "Honolulu DEM", TowerTypeEmergency, 21.3000, -157.8600, "155.340 MHz", "", "", "Honolulu DEM", "Honolulu", "HI", "US", 30},
	{"EY-2M", "El Yunque", TowerTypeRepeater, 18.3110, -65.7900, "146.910 MHz", "-0.6 MHz", "100.0 Hz", "KP4RG", "Rio Grande", "PR", "US", 1050},

	// ---------------------------------------------------------------------
	// China: Beijing, Shanghai, and the eastern provinces
	// ---------------------------------------------------------------------
	{"BJ-RPT-01", "Xiangshan Relay", TowerTypeRepeater, 39.9936, 116.1883, "439.500 MHz", "-5.0 MHz", "88.5 Hz", "BY1BJ", "Beijing", "BJ", "CN", 550},
	{"BJ-RPT-02", "Miaofengshan Relay", TowerTypeRepeater, 40.0600, 116.0200, "145.300 MHz", "-0.6 MHz", "88.5 Hz", "BY1BJ", "Beijing", "BJ", "CN", 1290},
	{"BJ-RPT-03", "Beijing Central Radio Tower", TowerTypeRepeater, 39.9183, 116.4667, "438.500 MHz", "-5.0 MHz", "88.5 Hz", "BY1CRA", "Beijing", "BJ", "CN", 405},
	{"BJ-RPT-04", "Wuling Mountain Relay", TowerTypeRepeater, 40.6000, 117.3800, "145.700 MHz", "-0.6 MHz", "100.0 Hz", "BY1BJ", "Beijing", "BJ", "CN", 1100},
	{"BJ-E001", "Beijing Emergency Comms", TowerTypeEmergency, 39.9042, 116.4074, "439.000 MHz", "-5.0 MHz", "88.5 Hz", "CEA", "Beijing", "BJ", "CN", 45},
	{"CN-BJ-T001", "China Mobile Beijing Mast", TowerTypeCellular, 39.9088, 116.3975, "Band 3/8/41", "", "", "China Mobile", "Beijing", "BJ", "CN", 45},
	{"SH-RPT-01", "Shanghai Tower Relay", TowerTypeRepeater, 31.2335, 121.5015, "439.625 MHz", "-5.0 MHz", "88.5 Hz", "BY4CRA", "Shanghai", "SH", "CN", 632},
	{"SH-RPT-02", "Sheshan Relay", TowerTypeRepeater, 31.0981, 121.1897, "145.600 MHz", "-0.6 MHz", "94.8 Hz", "BY4CRA", "Shanghai", "SH", "CN", 98},
	{"SH-RPT-03", "Oriental Pearl Relay", TowerTypeRepeater, 31.2397, 121.4998, "438.900 MHz", "-5.0 MHz", "88.5 Hz", "BY4AA", "Shanghai", "SH", "CN", 468},
	{"SH-E001", "Shanghai Emergency Relay", TowerTypeEmergency, 31.2304, 121.4737, "439.100 MHz", "-5.0 MHz", "88.5 Hz", "SH-RC", "Shanghai", "SH", "CN", 12},
	{"CN-SH-T001", "China Unicom Shanghai Mast", TowerTypeCellular, 31.2304, 121.4737, "Band 1/3/41", "", "", "China Unicom", "Shanghai", "SH", "CN", 12},
	{"CN-SH-M001", "Yangshan Port VTS", TowerTypeMaritime, 30.6300, 122.0700, "156.800 MHz", "", "", "MSA", "Shanghai", "SH", "CN", 10},
	{"GD-RPT-01", "Canton Tower Relay", TowerTypeRepeater, 23.1066, 113.3245, "439.750 MHz", "-5.0 MHz", "88.5 Hz", "BY7CRA", "Guangzhou", "GD", "CN", 604},
	{"GD-RPT-02", "Baiyun Mountain Relay", TowerTypeRepeater, 23.1833, 113.3000, "145.550 MHz", "-0.6 MHz", "88.5 Hz", "BY7AA", "Guangzhou", "GD", "CN", 382},
	{"GD-RPT-03", "Wutong Mountain Relay", TowerTypeRepeater, 22.5900, 114.2100, "439.500 MHz", "-5.0 MHz", "88.5 Hz", "BY7SZ", "Shenzhen", "GD", "CN", 943},
	{"GD-RPT-04", "Ping An Finance Relay", TowerTypeRepeater, 22.5366, 114.0500, "438.800 MHz", "-5.0 MHz", "88.5 Hz", "BY7SZ", "Shenzhen", "GD", "CN", 660},
	{"GD-RPT-05", "Huangqishan Relay", TowerTypeRepeater, 22.9000, 113.8500, "145.650 MHz", "-0.6 MHz", "88.5 Hz", "BY7DG", "Dongguan", "GD", "CN", 350},
	{"GD-RPT-06", "Phoenix Mountain Relay", TowerTypeRepeater, 22.2900, 113.5300, "439.300 MHz", "-5.0 MHz", "94.8 Hz", "BY7ZH", "Zhuhai", "GD", "CN", 437},
	{"GD-E001", "Guangdong Emergency Net", TowerTypeEmergency, 23.1291, 113.2644, "439.950 MHz", "-5.0 MHz", "88.5 Hz", "GD-EM", "Guangzhou", "GD", "CN", 20},
	{"CN-GD-T001", "China Telecom Shenzhen Mast", TowerTypeCellular, 22.5431, 114.0579, "Band 5/8/41", "", "", "China Telecom", "Shenzhen", "GD", "CN", 25},
	{"CN-GD-M001", "Pearl River VTS", TowerTypeMaritime, 23.0900, 113.4500, "156.700 MHz", "", "", "MSA", "Guangzhou", "GD", "CN", 8},
	{"HE-RPT-01", "Shijiazhuang Relay", TowerTypeRepeater, 38.0400, 114.5100, "439.600 MHz", "-5.0 MHz", "88.5 Hz", "BY3CRA", "Shijiazhuang", "HE", "CN", 80},
	{"HE-RPT-02", "Chengde Relay", TowerTypeRepeater, 40.9800, 117.9400, "145.500 MHz", "-0.6 MHz", "88.5 Hz", "BY3CD", "Chengde", "HE", "CN", 350},
	{"HA-RPT-01", "Songshan Relay", TowerTypeRepeater, 34.5000, 113.0300, "439.500 MHz", "-5.0 MHz", "88.5 Hz", "BY6SS", "Dengfeng", "HA", "CN", 1512},
	{"HA-RPT-02", "Zhengzhou Relay", TowerTypeRepeater, 34.7500, 113.6300, "145.550 MHz", "-0.6 MHz", "88.5 Hz", "BY6CRA", "Zhengzhou", "HA", "CN", 110},
	{"SD-RPT-01", "Taishan Relay", TowerTypeRepeater, 36.2500, 117.1000, "439.650 MHz", "-5.0 MHz", "88.5 Hz", "BY4TA", "Tai'an", "SD", "CN", 1545},
	{"SD-RPT-02", "Qingdao Relay", TowerTypeRepeater, 36.0700, 120.3800, "145.600 MHz", "-0.6 MHz", "88.5 Hz", "BY4QD", "Qingdao", "SD", "CN", 384},
	{"JS-RPT-01", "Zijin Mountain Relay", TowerTypeRepeater, 32.0700, 118.8400, "439.700 MHz", "-5.0 MHz", "88.5 Hz", "BY4CRA", "Nanjing", "JS", "CN", 448},
	{"JS-RPT-02", "Suzhou Relay", TowerTypeRepeater, 31.3000, 120.6000, "145.500 MHz", "-0.6 MHz", "88.5 Hz", "BY4SZ", "Suzhou", "JS", "CN", 12},
	{"ZJ-RPT-01", "Hangzhou Relay", TowerTypeRepeater, 30.2500, 120.1600, "439.550 MHz", "-5.0 MHz", "88.5 Hz", "BY5CRA", "Hangzhou", "ZJ", "CN", 42},
	{"ZJ-RPT-02", "Ningbo Relay", TowerTypeRepeater, 29.8700, 121.5500, "145.450 MHz", "-0.6 MHz", "88.5 Hz", "BY5NB", "Ningbo", "ZJ", "CN", 30},
	{"AH-RPT-01", "Huangshan Relay", TowerTypeRepeater, 30.1300, 118.1700, "439.500 MHz", "-5.0 MHz", "88.5 Hz", "BY6HS", "Huangshan", "AH", "CN", 1864},
	{"AH-RPT-02", "Hefei Relay", TowerTypeRepeater, 31.8200, 117.2300, "145.500 MHz", "-0.6 MHz", "88.5 Hz", "BY6HF", "Hefei", "AH", "CN", 40},
	{"FJ-RPT-01", "Wuyi Mountain Relay", TowerTypeRepeater, 27.7500, 117.6800, "439.400 MHz", "-5.0 MHz", "88.5 Hz", "BY5WY", "Wuyishan", "FJ", "CN", 2158},
	{"FJ-RPT-02", "Xiamen Relay", TowerTypeRepeater, 24.4800, 118.0900, "145.550 MHz", "-0.6 MHz", "88.5 Hz", "BY5XM", "Xiamen", "FJ", "CN", 340},
	{"HN-RPT-01", "Hengshan Relay", TowerTypeRepeater, 27.2500, 112.6800, "439.500 MHz", "-5.0 MHz", "88.5 Hz", "BY7HS", "Hengyang", "HN", "CN", 1300},
	{"HN-RPT-02", "Changsha Relay", TowerTypeRepeater, 28.2300, 112.9400, "145.500 MHz", "-0.6 MHz", "88.5 Hz", "BY7CRA", "Changsha", "HN", "CN", 60},
	{"HB-RPT-01", "Wuhan Relay", TowerTypeRepeater, 30.5800, 114.3000, "439.600 MHz", "-5.0 MHz", "88.5 Hz", "BY6CRA", "Wuhan", "HB", "CN", 40},
	{"HB-RPT-02", "Shennongjia Relay", TowerTypeRepeater, 31.7500, 110.6800, "145.600 MHz", "-0.6 MHz", "88.5 Hz", "BY6SN", "Shennongjia", "HB", "CN", 3105},
	{"JX-RPT-01", "Lushan Relay", TowerTypeRepeater, 29.5500, 115.9800, "439.450 MHz", "-5.0 MHz", "88.5 Hz", "BY5LS", "Jiujiang", "JX", "CN", 1474},
	{"GX-RPT-01", "Guilin Relay", TowerTypeRepeater, 25.2700, 110.2900, "439.500 MHz", "-5.0 MHz", "88.5 Hz", "BY7GL", "Guilin", "GX", "CN", 150},
	{"GX-RPT-02", "Nanning Relay", TowerTypeRepeater, 22.8200, 108.3200, "145.550 MHz", "-0.6 MHz", "88.5 Hz", "BY7CRA", "Nanning", "GX", "CN", 76},
	{"GZ-RPT-01", "Guiyang Relay", TowerTypeRepeater, 26.6500, 106.6300, "439.550 MHz", "-5.0 MHz", "88.5 Hz", "BY8GY", "Guiyang", "GZ", "CN", 1100},
	{"SX-RPT-01", "Wutai Mountain Relay", TowerTypeRepeater, 39.0000, 113.5900, "439.400 MHz", "-5.0 MHz", "88.5 Hz", "BY3WT", "Wutai", "SX", "CN", 3061},
	{"NM-RPT-01", "Hohhot Relay", TowerTypeRepeater, 40.8400, 111.7500, "439.600 MHz", "-5.0 MHz", "88.5 Hz", "BY3HH", "Hohhot", "NM", "CN", 1040},
	{"NM-RPT-02", "Baotou Relay", TowerTypeRepeater, 40.6600, 109.8400, "145.500 MHz", "-0.6 MHz", "88.5 Hz", "BY3BT", "Baotou", "NM", "CN", 1065},

	// ---------------------------------------------------------------------
	// China: the northeast, the northwest, and the southwest
	// ---------------------------------------------------------------------
	{"LN-RPT-01", "Shenyang Relay", TowerTypeRepeater, 41.8000, 123.4300, "439.550 MHz", "-5.0 MHz", "88.5 Hz", "BY2CRA", "Shenyang", "LN", "CN", 45},
	{"LN-RPT-02", "Dalian Relay", TowerTypeRepeater, 38.9200, 121.6300, "145.550 MHz", "-0.6 MHz", "88.5 Hz", "BY2DL", "Dalian", "LN", "CN", 90},
	{"JL-RPT-01", "Changchun Relay", TowerTypeRepeater, 43.8800, 125.3200, "439.500 MHz", "-5.0 MHz", "88.5 Hz", "BY2CC", "Changchun", "JL", "CN", 220},
	{"HL-RPT-01", "Harbin Relay", TowerTypeRepeater, 45.7600, 126.6400, "439.600 MHz", "-5.0 MHz", "88.5 Hz", "BY2CRA", "Harbin", "HL", "CN", 150},
	{"HL-RPT-02", "Mohe Relay", TowerTypeRepeater, 52.9700, 122.5400, "145.500 MHz", "-0.6 MHz", "88.5 Hz", "BY2MH", "Mohe", "HL", "CN", 438},
	{"XJ-RPT-01", "Urumqi Relay", TowerTypeRepeater, 43.8256, 87.6168, "439.550 MHz", "-5.0 MHz", "88.5 Hz", "BY0CRA", "Urumqi", "XJ", "CN", 800},
	{"XJ-RPT-02", "Kashgar Relay", TowerTypeRepeater, 39.4700, 75.9900, "145.550 MHz", "-0.6 MHz", "88.5 Hz", "BY0KS", "Kashgar", "XJ", "CN", 1290},
	{"XJ-RPT-03", "Turpan Relay", TowerTypeRepeater, 42.9500, 89.1900, "439.400 MHz", "-5.0 MHz", "88.5 Hz", "BY0TP", "Turpan", "XJ", "CN", 35},
	{"XJ-RPT-04", "Korla Relay", TowerTypeRepeater, 41.7300, 86.1500, "145.500 MHz", "-0.6 MHz", "88.5 Hz", "BY0KR", "Korla", "XJ", "CN", 950},
	{"XJ-RPT-05", "Hotan Relay", TowerTypeRepeater, 37.1100, 79.9200, "439.300 MHz", "-5.0 MHz", "88.5 Hz", "BY0HT", "Hotan", "XJ", "CN", 1375},
	{"CN-XJ-T001", "China Unicom Urumqi Mast", TowerTypeCellular, 43.8256, 87.6168, "Band 1/3/41", "", "", "China Unicom", "Urumqi", "XJ", "CN", 800},
	{"GS-RPT-01", "Lanzhou Relay", TowerTypeRepeater, 36.0600, 103.8300, "439.550 MHz", "-5.0 MHz", "88.5 Hz", "BY9LZ", "Lanzhou", "GS", "CN", 1520},
	{"GS-RPT-02", "Jiayuguan Relay", TowerTypeRepeater, 39.8000, 98.2900, "145.550 MHz", "-0.6 MHz", "88.5 Hz", "BY9JY", "Jiayuguan", "GS", "CN", 1620},
	{"GS-RPT-03", "Dunhuang Relay", TowerTypeRepeater, 40.1400, 94.6600, "439.250 MHz", "-5.0 MHz", "88.5 Hz", "BY9DH", "Dunhuang", "GS", "CN", 1139},
	{"QH-RPT-01", "Xining Relay", TowerTypeRepeater, 36.6171, 101.7782, "439.500 MHz", "-5.0 MHz", "88.5 Hz", "BY9CRA", "Xining", "QH", "CN", 2261},
	{"QH-RPT-02", "Qinghai Lake Relay", TowerTypeRepeater, 36.9000, 100.2000, "145.500 MHz", "-0.6 MHz", "88.5 Hz", "BY9QH", "Qinghai Lake", "QH", "CN", 3200},
	{"QH-RPT-03", "Golmud Relay", TowerTypeRepeater, 36.4000, 94.9000, "439.350 MHz", "-5.0 MHz", "88.5 Hz", "BY9GM", "Golmud", "QH", "CN", 2780},
	{"QH-RPT-04", "Yushu Relay", TowerTypeRepeater, 33.0000, 97.0100, "145.700 MHz", "-0.6 MHz", "88.5 Hz", "BY9YS", "Yushu", "QH", "CN", 3700},
	{"XZ-RPT-01", "Lhasa Relay", TowerTypeRepeater, 29.6520, 91.1721, "439.850 MHz", "-5.0 MHz", "88.5 Hz", "BY0CRA", "Lhasa", "XZ", "CN", 3650},
	{"XZ-RPT-02", "Nyingchi Relay", TowerTypeRepeater, 29.6500, 94.3600, "145.450 MHz", "-0.6 MHz", "88.5 Hz", "BY0LZ", "Nyingchi", "XZ", "CN", 2990},
	{"XZ-RPT-03", "Tanggula Pass Relay", TowerTypeRepeater, 32.8900, 91.9400, "439.200 MHz", "-5.0 MHz", "88.5 Hz", "BY0TG", "Tanggula", "XZ", "CN", 5231},
	{"XZ-RPT-04", "Nagqu Relay", TowerTypeRepeater, 31.4800, 92.0500, "145.600 MHz", "-0.6 MHz", "88.5 Hz", "BY0NQ", "Nagqu", "XZ", "CN", 4507},
	{"XZ-RPT-05", "Qamdo Relay", TowerTypeRepeater, 31.1400, 97.1700, "439.400 MHz", "-5.0 MHz", "88.5 Hz", "BY0CD", "Qamdo", "XZ", "CN", 3240},
	{"CN-XZ-T001", "China Mobile Lhasa Mast", TowerTypeCellular, 29.6500, 91.1000, "Band 3/8/41", "", "", "China Mobile", "Lhasa", "XZ", "CN", 3650},
	{"SC-RPT-01", "Longquanshan Relay", TowerTypeRepeater, 30.5600, 104.2800, "439.850 MHz", "-5.0 MHz", "88.5 Hz", "BY8CRA", "Chengdu", "SC", "CN", 1051},
	{"SC-RPT-02", "Qingcheng Mountain Relay", TowerTypeRepeater, 30.9000, 103.5700, "145.450 MHz", "-0.6 MHz", "88.5 Hz", "BY8AA", "Dujiangyan", "SC", "CN", 1260},
	{"SC-RPT-03", "Emei Mountain Relay", TowerTypeRepeater, 29.5200, 103.3300, "439.650 MHz", "-5.0 MHz", "88.5 Hz", "BY8EM", "Leshan", "SC", "CN", 3099},
	{"SC-RPT-04", "Xiling Snow Mountain Relay", TowerTypeRepeater, 30.7500, 103.1800, "145.800 MHz", "-0.6 MHz", "88.5 Hz", "BY8AA", "Chengdu", "SC", "CN", 5364},
	{"SC-RPT-05", "Kangding Relay", TowerTypeRepeater, 30.0500, 101.9600, "439.400 MHz", "-5.0 MHz", "88.5 Hz", "BY8KD", "Kangding", "SC", "CN", 2560},
	{"SC-RPT-06", "Litang Relay", TowerTypeRepeater, 29.9900, 100.2700, "145.500 MHz", "-0.6 MHz", "88.5 Hz", "BY8LT", "Litang", "SC", "CN", 4014},
	{"SC-RPT-07", "Batang Relay", TowerTypeRepeater, 30.0000, 99.1000, "439.200 MHz", "-5.0 MHz", "88.5 Hz", "BY8BT", "Batang", "SC", "CN", 2589},
	{"SC-RPT-08", "Daocheng Relay", TowerTypeRepeater, 29.0300, 100.3000, "145.900 MHz", "-0.6 MHz", "88.5 Hz", "BY8DC", "Daocheng", "SC", "CN", 3750},
	{"SC-RPT-09", "Ya'an Relay", TowerTypeRepeater, 29.9800, 103.0000, "439.700 MHz", "-5.0 MHz", "88.5 Hz", "BY8YA", "Ya'an", "SC", "CN", 620},
	{"SC-E001", "Sichuan Emergency Relay", TowerTypeEmergency, 30.5728, 104.0668, "439.000 MHz", "-5.0 MHz", "88.5 Hz", "SC-EM", "Chengdu", "SC", "CN", 500},
	{"CN-SC-T001", "China Mobile Chengdu Mast", TowerTypeCellular, 30.5728, 104.0668, "Band 3/8/41", "", "", "China Mobile", "Chengdu", "SC", "CN", 500},
	{"CQ-RPT-01", "Nanshan Relay", TowerTypeRepeater, 29.5400, 106.5800, "439.550 MHz", "-5.0 MHz", "88.5 Hz", "BY8CQ", "Chongqing", "CQ", "CN", 680},
	{"CQ-RPT-02", "Jinyun Mountain Relay", TowerTypeRepeater, 29.8300, 106.3800, "145.400 MHz", "-0.6 MHz", "88.5 Hz", "BY8CQ", "Chongqing", "CQ", "CN", 1050},
	{"SN-RPT-01", "Zhongnan Mountain Relay", TowerTypeRepeater, 34.0000, 108.9000, "439.600 MHz", "-5.0 MHz", "88.5 Hz", "BY9CRA", "Xi'an", "SN", "CN", 1500},
	{"SN-RPT-02", "Huashan Relay", TowerTypeRepeater, 34.4800, 110.0800, "145.600 MHz", "-0.6 MHz", "88.5 Hz", "BY9HS", "Huayin", "SN", "CN", 2154},
	{"SN-RPT-03", "Taibai Mountain Relay", TowerTypeRepeater, 34.0000, 107.7700, "439.350 MHz", "-5.0 MHz", "88.5 Hz", "BY9TB", "Baoji", "SN", "CN", 3767},
	{"YN-RPT-01", "Xishan Relay", TowerTypeRepeater, 24.9600, 102.6300, "439.500 MHz", "-5.0 MHz", "88.5 Hz", "BY8CRA", "Kunming", "YN", "CN", 2350},
	{"YN-RPT-02", "Cangshan Relay", TowerTypeRepeater, 25.6500, 100.1700, "145.500 MHz", "-0.6 MHz", "88.5 Hz", "BY8DL", "Dali", "YN", "CN", 4122},
	{"YN-RPT-03", "Yulong Snow Mountain Relay", TowerTypeRepeater, 27.1000, 100.1800, "439.300 MHz", "-5.0 MHz", "88.5 Hz", "BY8LJ", "Lijiang", "YN", "CN", 4506},
	{"YN-RPT-04", "Tengchong Relay", TowerTypeRepeater, 25.0200, 98.4900, "145.700 MHz", "-0.6 MHz", "88.5 Hz", "BY8TC", "Tengchong", "YN", "CN", 1640},
	{"HI-RPT-01", "Haikou Relay", TowerTypeRepeater, 20.0300, 110.3200, "439.500 MHz", "-5.0 MHz", "88.5 Hz", "BY7HK", "Haikou", "HI", "CN", 15},
	{"HI-RPT-02", "Sanya Relay", TowerTypeRepeater, 18.2500, 109.5100, "145.550 MHz", "-0.6 MHz", "88.5 Hz", "BY7SY", "Sanya", "HI", "CN", 20},
	{"CN-HI-M001", "Sanya Marine VHF", TowerTypeMaritime, 18.2300, 109.5000, "156.800 MHz", "", "", "MSA", "Sanya", "HI", "CN", 6},

	// ---------------------------------------------------------------------
	// International hubs: Europe, Asia-Pacific, the Americas, and Africa
	// ---------------------------------------------------------------------
	{"GB-LON-01", "BT Tower Relay", TowerTypeRepeater, 51.5215, -0.1387, "145.725 MHz", "-0.6 MHz", "82.5 Hz", "G0LON", "London", "ENG", "GB", 189},
	{"GB-LON-02", "Crystal Palace Relay", TowerTypeRepeater, 51.4242, -0.0757, "439.600 MHz", "-5.0 MHz", "82.5 Hz", "G3CP", "London", "ENG", "GB", 112},
	{"GB-WAL-01", "Snowdon Relay", TowerTypeRepeater, 53.0685, -4.0764, "145.650 MHz", "-0.6 MHz", "110.9 Hz", "GW0SN", "Llanberis", "WLS", "GB", 1085},
	{"GB-SCT-01", "Ben Nevis Relay", TowerTypeRepeater, 56.7969, -5.0036, "145.600 MHz", "-0.6 MHz", "118.8 Hz", "GM0BN", "Fort William", "SCT", "GB", 1345},
	{"GB-MEN-01", "Mendip Relay", TowerTypeRepeater, 51.2400, -2.6300, "439.550 MHz", "-5.0 MHz", "82.5 Hz", "G3MD", "Bristol", "ENG", "GB", 305},
	{"GB-NIR-01", "Belfast Relay", TowerTypeRepeater, 54.5973, -5.9301, "145.700 MHz", "-0.6 MHz", "82.5 Hz", "GI0B", "Belfast", "NIR", "GB", 60},
	{"DE-BER-01", "Berlin Fernsehturm Relay", TowerTypeRepeater, 52.5208, 13.4094, "439.650 MHz", "-5.0 MHz", "67.0 Hz", "DB0BER", "Berlin", "BE", "DE", 368},
	{"DE-BRK-01", "Brocken Relay", TowerTypeRepeater, 51.7992, 10.6156, "438.500 MHz", "-5.0 MHz", "67.0 Hz", "DB0BRK", "Wernigerode", "SA", "DE", 1141},
	{"DE-FEL-01", "Feldberg Relay", TowerTypeRepeater, 47.8739, 8.0044, "439.400 MHz", "-5.0 MHz", "71.9 Hz", "DB0FEL", "Freiburg", "BW", "DE", 1493},
	{"DE-ZUG-01", "Zugspitze Relay", TowerTypeRepeater, 47.4211, 10.9864, "145.700 MHz", "-0.6 MHz", "67.0 Hz", "DB0ZUG", "Grainau", "BY", "DE", 2962},
	{"FR-PAR-01", "Eiffel Tower Relay", TowerTypeRepeater, 48.8584, 2.2945, "439.600 MHz", "-5.0 MHz", "71.9 Hz", "F5PAR", "Paris", "IDF", "FR", 324},
	{"FR-VEN-01", "Mont Ventoux Relay", TowerTypeRepeater, 44.1739, 5.2783, "145.650 MHz", "-0.6 MHz", "71.9 Hz", "F5VEN", "Carpentras", "PACA", "FR", 1910},
	{"FR-PDD-01", "Puy de Dome Relay", TowerTypeRepeater, 45.7722, 2.9647, "439.500 MHz", "-5.0 MHz", "71.9 Hz", "F5PDD", "Clermont-Ferrand", "ARA", "FR", 1465},
	{"ES-MAD-01", "Madrid Relay", TowerTypeRepeater, 40.4168, -3.7038, "145.700 MHz", "-0.6 MHz", "88.5 Hz", "EA4MAD", "Madrid", "MD", "ES", 650},
	{"IT-ROM-01", "Rome Relay", TowerTypeRepeater, 41.9028, 12.4964, "439.600 MHz", "-5.0 MHz", "88.5 Hz", "IZ0ROM", "Rome", "LAZ", "IT", 50},
	{"NO-OSL-01", "Oslo Relay", TowerTypeRepeater, 59.9139, 10.7522, "145.650 MHz", "-0.6 MHz", "88.5 Hz", "LA1OSL", "Oslo", "OSL", "NO", 120},
	{"SE-STO-01", "Stockholm Relay", TowerTypeRepeater, 59.3293, 18.0686, "439.650 MHz", "-5.0 MHz", "88.5 Hz", "SM0STO", "Stockholm", "STO", "SE", 60},
	{"CH-ZRH-01", "Zurich Relay", TowerTypeRepeater, 47.3769, 8.5417, "145.700 MHz", "-0.6 MHz", "88.5 Hz", "HB9ZRH", "Zurich", "ZH", "CH", 408},
	{"IS-RKV-01", "Reykjavik Relay", TowerTypeRepeater, 64.1466, -21.9426, "439.700 MHz", "-5.0 MHz", "88.5 Hz", "TF3RKV", "Reykjavik", "RKV", "IS", 60},
	{"JP-TKY-01", "Tokyo Skytree Relay", TowerTypeRepeater, 35.7101, 139.8107, "439.660 MHz", "-5.0 MHz", "67.0 Hz", "JJ1YMT", "Tokyo", "TOK", "JP", 634},
	{"JP-TKY-02", "Tokyo Tower Relay", TowerTypeRepeater, 35.6586, 139.7454, "145.700 MHz", "-0.6 MHz", "67.0 Hz", "JQ1YMT", "Tokyo", "TOK", "JP", 333},
	{"JP-FJI-01", "Mount Fuji Relay", TowerTypeRepeater, 35.3606, 138.7274, "439.500 MHz", "-5.0 MHz", "67.0 Hz", "JJ1YAF", "Fujinomiya", "SZK", "JP", 3776},
	{"JP-TSU-01", "Mount Tsukuba Relay", TowerTypeRepeater, 36.2256, 140.1067, "145.650 MHz", "-0.6 MHz", "67.0 Hz", "JQ1YTK", "Tsukuba", "IBR", "JP", 877},
	{"JP-OSA-01", "Umeda Relay", TowerTypeRepeater, 34.7050, 135.4900, "439.700 MHz", "-5.0 MHz", "67.0 Hz", "JP3OSK", "Osaka", "OSK", "JP", 173},
	{"IN-DEL-01", "Qutub Minar Relay", TowerTypeRepeater, 28.5245, 77.1855, "145.700 MHz", "-0.6 MHz", "88.5 Hz", "VU2DEL", "New Delhi", "DL", "IN", 240},
	{"IN-MUM-01", "Mumbai Relay", TowerTypeRepeater, 19.0760, 72.8777, "439.600 MHz", "-5.0 MHz", "88.5 Hz", "VU2MUM", "Mumbai", "MH", "IN", 20},
	{"AE-DXB-01", "Burj Khalifa Relay", TowerTypeRepeater, 25.1972, 55.2744, "439.700 MHz", "-5.0 MHz", "88.5 Hz", "A61DXB", "Dubai", "DU", "AE", 828},
	{"AU-SYD-01", "Sydney Tower Relay", TowerTypeRepeater, -33.8704, 151.2092, "439.700 MHz", "-5.0 MHz", "91.5 Hz", "VK2SYD", "Sydney", "NSW", "AU", 309},
	{"AU-BNE-01", "Mount Coot-tha Relay", TowerTypeRepeater, -27.4753, 152.9600, "145.650 MHz", "-0.6 MHz", "91.5 Hz", "VK4BNE", "Brisbane", "QLD", "AU", 287},
	{"AU-MEL-01", "Mount Dandenong Relay", TowerTypeRepeater, -37.8300, 145.3550, "439.650 MHz", "-5.0 MHz", "91.5 Hz", "VK3MEL", "Melbourne", "VIC", "AU", 633},
	{"AU-CBR-01", "Black Mountain Relay", TowerTypeRepeater, -35.2750, 149.0980, "145.700 MHz", "-0.6 MHz", "91.5 Hz", "VK1CBR", "Canberra", "ACT", "AU", 812},
	{"AU-PER-01", "Kings Park Relay", TowerTypeRepeater, -31.9600, 115.8300, "439.600 MHz", "-5.0 MHz", "91.5 Hz", "VK6PER", "Perth", "WA", "AU", 60},
	{"NZ-AKL-01", "Sky Tower Relay", TowerTypeRepeater, -36.8485, 174.7633, "439.700 MHz", "-5.0 MHz", "88.5 Hz", "ZL1AKL", "Auckland", "AUK", "NZ", 328},
	{"NZ-WLG-01", "Mount Victoria Relay", TowerTypeRepeater, -41.2960, 174.7930, "145.650 MHz", "-0.6 MHz", "88.5 Hz", "ZL2WLG", "Wellington", "WGN", "NZ", 196},
	{"CA-TOR-01", "CN Tower Relay", TowerTypeRepeater, 43.6426, -79.3871, "439.650 MHz", "-5.0 MHz", "103.5 Hz", "VE3TOR", "Toronto", "ON", "CA", 553},
	{"CA-MTL-01", "Mount Royal Relay", TowerTypeRepeater, 45.5089, -73.5878, "145.700 MHz", "-0.6 MHz", "103.5 Hz", "VE2MTL", "Montreal", "QC", "CA", 234},
	{"BR-RIO-01", "Sugarloaf Relay", TowerTypeRepeater, -22.9490, -43.1545, "439.600 MHz", "-5.0 MHz", "88.5 Hz", "PY1RIO", "Rio de Janeiro", "RJ", "BR", 396},
	{"BR-SAO-01", "Sao Paulo Relay", TowerTypeRepeater, -23.5505, -46.6333, "145.650 MHz", "-0.6 MHz", "88.5 Hz", "PY2SAO", "Sao Paulo", "SP", "BR", 760},
	{"ZA-CPT-01", "Table Mountain Relay", TowerTypeRepeater, -33.9628, 18.4098, "439.650 MHz", "-5.0 MHz", "88.5 Hz", "ZS1CPT", "Cape Town", "WC", "ZA", 1085},
	{"KE-NBO-01", "Nairobi Relay", TowerTypeRepeater, -1.2921, 36.8219, "145.650 MHz", "-0.6 MHz", "88.5 Hz", "5Z4NBO", "Nairobi", "NBO", "KE", 1795},
}
