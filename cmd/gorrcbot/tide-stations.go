// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file holds the reference table of tide stations: the ports an operator
// is most likely to name, with their provider station id, name, and position.
// The ids, names, and coordinates are the provider's own station catalog,
// selected once and never edited by hand; tide.go is the only reader.
//
// The table exists so that a position can be resolved to the nearest station
// without a second network call, which is what makes "tide near me" work on a
// link that can afford exactly one request. A station id that is not in the
// table is still accepted: the provider is authoritative about its own stations,
// and the table is only a shortcut for finding the right one.

package main

// tideStation is one reference tide station.
type tideStation struct {
	// ID is the provider's station identifier.
	ID string
	// Name is the station's published name.
	Name string
	// State is the two-letter state or territory code, empty for a station
	// outside the states.
	State string
	// Lat and Lng are the station's position in signed decimal degrees.
	Lat float64
	Lng float64
}

// tideStations is the reference table, ordered by station id.
var tideStations = []tideStation{
	{"9410135", "South San Diego Bay", "CA", 32.629101, -117.107803},
	{"9410660", "Los Angeles (Outer Harbor)", "CA", 33.720000, -118.272000},
	{"9410840", "Santa Monica, Municipal Pier", "CA", 34.008300, -118.500000},
	{"9410678", "Long Beach Fire Boat Pier", "CA", 33.747799, -118.215797},
	{"9412110", "Port San Luis", "CA", 35.168889, -120.754167},
	{"9413450", "Monterey, Monterey Bay", "CA", 36.608889, -121.891389},
	{"9414290", "San Francisco (Golden Gate)", "CA", 37.806306, -122.465889},
	{"9414764", "Oakland Inner Harbor", "CA", 37.794998, -122.281998},
	{"9414750", "Alameda", "CA", 37.771953, -122.300261},
	{"9416131", "Port of West Sacramento", "CA", 38.562248, -121.546303},
	{"9418767", "Humboldt Bay (North Spit)", "CA", 40.766906, -124.217344},
	{"9419750", "Crescent City", "CA", 41.745611, -124.184389},
	{"9432845", "Coos Bay", "OR", 43.380000, -124.215000},
	{"9435308", "Weiser Point, Yaquina River", "OR", 44.593300, -124.008000},
	{"9439040", "Astoria (Tongue Point), Oreg.", "OR", 46.207306, -123.768303},
	{"9439221", "Portland Morrison Street Bridge", "OR", 45.510000, -122.673000},
	{"9441187", "Aberdeen", "WA", 46.968300, -123.853000},
	{"9441102", "Westport, Point Chehalis", "WA", 46.904310, -124.105083},
	{"9447130", "Seattle (Madison St.), Elliott Bay", "WA", 47.602639, -122.339306},
	{"9446484", "Tacoma, Commencement Bay, Sitcum Waterway", "WA", 47.266701, -122.413300},
	{"9447659", "Everett", "WA", 47.980000, -122.223000},
	{"9449211", "Bellingham", "WA", 48.745000, -122.495000},
	{"9444090", "Port Angeles", "WA", 48.125000, -123.440002},
	{"9449880", "Friday Harbor, San Juan Island", "WA", 48.545278, -123.012500},
	{"9448794", "Anacortes, Guemes Channel", "WA", 48.518300, -122.620000},
	{"9449424", "Cherry Point", "WA", 48.862717, -122.758583},
	{"9450460", "Ketchikan", "AK", 55.331944, -131.626111},
	{"9451600", "Sitka", "AK", 57.051056, -135.344116},
	{"9452210", "Juneau", "AK", 58.298800, -134.410600},
	{"9455090", "Seward, Resurrection Bay", "AK", 60.119300, -149.428108},
	{"9450544", "Hollis Anchorage", "AK", 55.480000, -132.645004},
	{"9457292", "Kodiak, Womens Bay", "AK", 57.731700, -152.512000},
	{"9462611", "Dutch Harbor, Amaknak Island", "AK", 53.891700, -166.537000},
	{"9468756", "Nome", "AK", 64.494611, -165.439639},
	{"9497645", "Prudhoe Bay", "AK", 70.411389, -148.531667},
	{"8779770", "Port Isabel", "TX", 26.061167, -97.215528},
	{"8775870", "Corpus Christi, Bob Hall Pier", "TX", 27.580000, -97.216698},
	{"8771510", "Galveston Pleasure Pier", "TX", 29.285300, -94.789400},
	{"8770570", "Sabine Pass", "TX", 29.728399, -93.870102},
	{"8767816", "Lake Charles", "LA", 30.223611, -93.221667},
	{"8768094", "Calcasieu Pass", "LA", 29.768167, -93.342889},
	{"8761724", "East Point, Grand Isle", "LA", 29.263300, -89.956703},
	{"8745557", "Gulfport Harbor, Mississippi Sound", "MS", 30.360000, -89.081700},
	{"8741533", "Pascagoula Noaa Lab", "MS", 30.367778, -88.563056},
	{"8733502", "Fly Creek, Mobile Bay", "AL", 30.542801, -87.901001},
	{"8729840", "Pensacola", "FL", 30.404400, -87.211197},
	{"8729108", "Panama City", "FL", 30.149722, -85.664444},
	{"8728690", "Apalachicola", "FL", 29.724444, -84.980556},
	{"8726607", "Old Port Tampa", "FL", 27.857800, -82.552803},
	{"8726520", "St. Petersburg", "FL", 27.760599, -82.626900},
	{"8726724", "Clearwater Beach", "FL", 27.978300, -82.831703},
	{"8727520", "Cedar Key", "FL", 29.135000, -83.031700},
	{"8725110", "Naples (Outer Coast)", "FL", 26.131667, -81.807500},
	{"8725520", "Fort Myers", "FL", 26.647778, -81.871111},
	{"8724580", "Key West", "FL", 24.555700, -81.807899},
	{"8723170", "Miami Harbor Entrance", "FL", 25.768300, -80.131700},
	{"8722956", "South Port Everglades, Icww", "FL", 26.081667, -80.116667},
	{"8722669", "Lake Worth Icw", "FL", 26.613300, -80.046700},
	{"8723970", "Vaca Key, Uscg Station, Florida Bay", "FL", 24.711000, -81.106500},
	{"8720220", "Mayport (Ferry Depot)", "FL", 30.393300, -81.431700},
	{"8720030", "Fernandina Beach, Amelia River", "FL", 30.671356, -81.465842},
	{"8721120", "Daytona Beach Shores, Sunglow Pier", "FL", 29.146700, -80.963300},
	{"8721604", "Port Canaveral (Trident Pier)", "FL", 28.415800, -80.593102},
	{"8670870", "Fort Pulaski, Savannah River Entrance", "GA", 32.034694, -80.903028},
	{"8665530", "Charleston (Customhouse Wharf)", "SC", 32.780833, -79.923611},
	{"8661070", "Springmaid Pier, Myrtle Beach", "SC", 33.654999, -78.918297},
	{"8667999", "Beaufort", "SC", 32.430000, -80.675000},
	{"8658120", "Wilmington", "NC", 34.226700, -77.953300},
	{"8658163", "Wrightsville Beach", "NC", 34.213306, -77.786694},
	{"8632200", "Kiptopeke Beach", "VA", 37.165199, -75.988403},
	{"8638610", "Hampton Roads (Sewells Point)", "VA", 36.942778, -76.328611},
	{"8637624", "Gloucester Point", "VA", 37.246700, -76.500000},
	{"8636580", "Windmill Point", "VA", 37.615500, -76.289778},
	{"8636941", "Richmond Deepwater Terminal, James River", "VA", 37.459556, -77.420778},
	{"8574680", "Baltimore, Fort Mchenry", "MD", 39.266693, -76.578308},
	{"8575512", "Annapolis (Us Naval Academy)", "MD", 38.983883, -76.480034},
	{"8571892", "Cambridge", "MD", 38.572500, -76.061667},
	{"8594900", "Washington, Washington Channel, D.C.", "DC", 38.873333, -77.021667},
	{"8557380", "Lewes (Breakwater Harbor)", "DE", 38.782833, -75.119278},
	{"8551910", "Reedy Point", "DE", 39.558333, -75.571944},
	{"8546252", "Bridesburg, Philadelphia, Pa.", "PA", 39.979683, -75.079317},
	{"8534720", "Atlantic City (Ocean)", "NJ", 39.356667, -74.418053},
	{"8531680", "Sandy Hook, Fort Hancock", "NJ", 40.466900, -74.009399},
	{"8518750", "New York (the Battery)", "NY", 40.700554, -74.014168},
	{"8516945", "Kings Point", "NY", 40.810299, -73.764900},
	{"8467150", "Bridgeport", "CT", 41.175819, -73.183969},
	{"8465705", "New Haven Harbor, New Haven Reach", "CT", 41.283298, -72.908302},
	{"8461490", "New London, State Pier", "CT", 41.371667, -72.095556},
	{"8452660", "Newport", "RI", 41.504333, -71.326139},
	{"8454000", "Providence, State Pier No.1", "RI", 41.807167, -71.400667},
	{"8449130", "Nantucket", "MA", 41.285000, -70.096703},
	{"8443970", "Boston", "MA", 42.353889, -71.050278},
	{"8423745", "Portsmouth", "NH", 43.078300, -70.751700},
	{"8418150", "Portland", "ME", 43.658056, -70.244167},
	{"8413320", "Bar Harbor", "ME", 44.392194, -68.204278},
	{"8415490", "Rockland", "ME", 44.105000, -69.101700},
	{"9755371", "San Juan", "PR", 18.458944, -66.116417},
	{"9759394", "Mayaguez", "PR", 18.218833, -67.162444},
	{"9757487", "Ponce", "PR", 17.969601, -66.619904},
	{"9751639", "Charlotte Amalie, St. Thomas Island", "VI", 18.330583, -64.925806},
	{"9751364", "Christiansted Harbor, St Croix", "VI", 17.747694, -64.698389},
	{"1612340", "Honolulu", "HI", 21.303333, -157.864528},
	{"1612401", "Ford Island, Pearl Harbor", "HI", 21.367500, -157.963898},
	{"1611400", "Nawiliwili", "HI", 21.954400, -159.356100},
	{"1615680", "Kahului", "HI", 20.894944, -156.469000},
	{"1617760", "Hilo", "HI", 19.730278, -155.055556},
	{"1617433", "Kawaihae", "HI", 20.036600, -155.829400},
	{"1630000", "Apra Harbor, Guam", "GU", 13.443389, 144.656361},
	{"1770000", "Pago Pago Harbor, Tutuila Island", "AS", -14.280000, -170.690002},
}

// catalogEntry renders this station as a searchable catalog row: the name a
// person would say, with the state it sits in.
func (s tideStation) catalogEntry() catalogEntry {
	label := s.Name
	if s.State != "" {
		label += ", " + s.State
	}
	return catalogEntry{
		ID:     s.ID,
		Label:  label,
		Region: s.State,
		Point:  LatLng{Lat: s.Lat, Lng: s.Lng},
	}
}

// tideCatalog is the reference table in the uniform form the discovery commands
// search. It is built once from the same table the tide command resolves a
// station against, so the lookup and the discovery can never disagree.
var tideCatalog = catalogFrom(tideStations, tideStation.catalogEntry)
