// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file holds the reference table of offshore weather buoys: the National
// Data Buoy Center stations whose realtime sea-state feed the buoy command
// reads, with the place each one reports from and where it floats.
//
// The table exists because a buoy id is opaque and nobody on a radio link
// should have to already know one. It is embedded rather than fetched, so
// "buoy near me" and "buoy search boston" answer offline, at the speed of the
// geodesy commands, and keep working when the provider is unreachable. The ids,
// names, and positions are the center's own active-station catalog, reduced to
// the stations that report weather; they are data, and the code in buoy.go is
// the only reader.
//
// The station list changes slowly: stations are commissioned and retired over
// years, never within a session. To refresh it, take the center's
// activestations.xml, keep the entries with met="y", and rewrite the rows
// below. A station id that is not in the table is still accepted by the buoy
// command, because the feed is authoritative about its own stations and the
// table is only a shortcut for finding the right one.

package bot

// buoyStation is one reference buoy.
type buoyStation struct {
	// ID is the station identifier the realtime feed takes, like 46026.
	ID string
	// Name is the place the buoy reports from, like San Francisco.
	Name string
	// Region is the state code, or the basin code for a buoy outside the
	// states (ATL, GOM, CAR, PAC).
	Region string
	// Lat and Lng are the buoy's position in signed decimal degrees.
	Lat float64
	Lng float64
}

// buoyStations is the reference table, ordered by station id.
var buoyStations = []buoyStation{
	{"41002", "South Hatteras", "NC", 31.743, -74.955},
	{"41004", "Edisto", "SC", 32.502, -79.099},
	{"41008", "Grays Reef", "GA", 31.4, -80.866},
	{"41009", "Canaveral", "FL", 28.508, -80.185},
	{"41010", "Canaveral East", "FL", 28.86, -78.478},
	{"41013", "Frying Pan Shoals", "NC", 33.436, -77.764},
	{"41025", "Diamond Shoals", "NC", 35.026, -75.38},
	{"41040", "North Equatorial One", "ATL", 14.568, -53.037},
	{"41041", "North Equatorial Two", "ATL", 14.259, -46.052},
	{"41043", "NE Puerto Rico", "PR", 21.09, -64.864},
	{"41044", "NE St Martin", "ATL", 21.582, -58.63},
	{"41046", "East Bahamas", "ATL", 23.84, -68.34},
	{"41047", "NE Bahamas", "ATL", 27.557, -71.48},
	{"41048", "West Bermuda", "ATL", 31.878, -69.722},
	{"41049", "South Bermuda", "ATL", 27.505, -62.271},
	{"42001", "Mid Gulf", "LA", 25.922, -89.638},
	{"42002", "West Gulf", "TX", 25.95, -93.78},
	{"42035", "Galveston", "TX", 29.235, -94.41},
	{"42036", "West Tampa", "FL", 28.5, -84.505},
	{"42039", "Pensacola", "FL", 28.768, -86.024},
	{"42055", "Bay of Campeche", "GOM", 22.14, -94.112},
	{"42056", "Yucatan Basin", "CAR", 19.82, -84.98},
	{"42057", "Western Caribbean", "CAR", 16.975, -81.578},
	{"42058", "Central Caribbean", "CAR", 14.114, -75.949},
	{"42060", "Caribbean Valley", "CAR", 16.428, -63.21},
	{"44007", "Portland", "ME", 43.525, -70.14},
	{"44008", "Nantucket", "MA", 40.5, -69.254},
	{"44011", "Georges Bank", "MA", 41.088, -66.546},
	{"44013", "Boston", "MA", 42.346, -70.651},
	{"44014", "Virginia Beach", "VA", 36.603, -74.837},
	{"44020", "Nantucket Sound", "MA", 41.497, -70.283},
	{"44025", "Long Island", "NY", 40.258, -73.175},
	{"44027", "Jonesport", "ME", 44.284, -67.301},
	{"44029", "Massachusetts Bay", "MA", 42.523, -70.566},
	{"44030", "Western Maine Shelf", "ME", 43.179, -70.426},
	{"44032", "Central Maine Shelf", "ME", 43.715, -69.355},
	{"44033", "Penobscot Bay", "ME", 44.055, -68.996},
	{"44034", "Eastern Maine Shelf", "ME", 44.103, -68.112},
	{"44065", "New York Harbor Entrance", "NY", 40.368, -73.701},
	{"45001", "Mid Superior", "MI", 48.061, -87.793},
	{"45002", "North Michigan", "MI", 45.344, -86.411},
	{"45004", "East Superior", "MI", 47.583, -86.586},
	{"45005", "West Erie", "OH", 41.677, -82.398},
	{"45006", "West Superior", "WI", 47.335, -89.793},
	{"45012", "East Lake America", "NY", 43.621, -77.401},
	{"45164", "Cleveland Buoy", "OH", 41.748, -81.698},
	{"45168", "South Haven Buoy", "MI", 42.397, -86.331},
	{"45170", "Michigan City Buoy", "IN", 41.755, -86.968},
	{"45174", "Wilmette Buoy", "IL", 42.135, -87.655},
	{"45175", "Mackinac Straits West, Mackinaw City", "MI", 45.825, -84.772},
	{"45186", "Waukegan Buoy", "IL", 42.368, -87.795},
	{"45198", "Chicago Buoy", "IL", 41.892, -87.563},
	{"46001", "Western Gulf of Alaska", "AK", 56.296, -148.027},
	{"46002", "West Oregon", "OR", 42.56, -130.523},
	{"46005", "West Washington", "WA", 46.147, -131.077},
	{"46006", "Southeast Papa", "PAC", 40.73, -137.42},
	{"46011", "Santa Maria", "CA", 34.937, -120.999},
	{"46012", "Half Moon Bay", "CA", 37.356, -122.881},
	{"46013", "Bodega Bay", "CA", 38.235, -123.317},
	{"46014", "Pt Arena", "CA", 39.225, -123.98},
	{"46015", "Port Orford", "OR", 42.754, -124.839},
	{"46022", "Eel River", "CA", 40.716, -124.54},
	{"46025", "Santa Monica Basin", "CA", 33.765, -119.077},
	{"46026", "San Francisco", "CA", 37.75, -122.838},
	{"46027", "St Georges", "CA", 41.84, -124.382},
	{"46028", "Cape San Martin", "CA", 35.763, -121.9},
	{"46029", "Columbia River Bar", "OR", 46.148, -124.508},
	{"46042", "Monterey", "CA", 36.787, -122.408},
	{"46047", "Tanner Bank", "CA", 32.418, -119.535},
	{"46050", "Stonewall Bank", "OR", 44.679, -124.535},
	{"46053", "East Santa Barbara", "CA", 34.246, -119.842},
	{"46054", "West Santa Barbara", "CA", 34.274, -120.468},
	{"46059", "West California", "PAC", 38.067, -129.895},
	{"46060", "West Orca Bay", "AK", 60.571, -146.795},
	{"46061", "Seal Rocks", "AK", 60.23, -146.837},
	{"46066", "South Kodiak", "AK", 52.776, -154.992},
	{"46069", "South Santa Rosa", "CA", 33.657, -120.227},
	{"46070", "Southwest Bering Sea", "AK", 55.048, 175.246},
	{"46071", "Western Aleutians", "AK", 51.035, 179.808},
	{"46072", "Central Aleutians", "AK", 51.645, -172.145},
	{"46073", "Southeast Bering Sea", "AK", 54.985, -171.874},
	{"46075", "Shumagin Islands", "AK", 53.93, -160.763},
	{"46076", "Cape Cleare", "AK", 59.508, -148.005},
	{"46077", "Shelikof Strait", "AK", 57.869, -154.211},
	{"46078", "Albatross Bank", "AK", 55.561, -152.599},
	{"46080", "Portlock Bank", "AK", 57.91, -150.129},
	{"46081", "Western Prince William Sound", "AK", 60.802, -148.283},
	{"46082", "Cape Suckling", "AK", 59.67, -143.353},
	{"46083", "Fairweather Ground", "AK", 58.276, -138.024},
	{"46084", "Cape Edgecumbe", "AK", 56.614, -136.04},
	{"46085", "Central Gulf of Alaska", "AK", 55.84, -142.895},
	{"46086", "San Clemente Basin", "CA", 32.504, -118.029},
	{"46087", "Neah Bay", "WA", 48.493, -124.727},
	{"46088", "New Dungeness", "WA", 48.332, -123.179},
	{"46089", "Tillamook", "OR", 45.928, -125.815},
	{"46108", "Lower Cook Inlet", "AK", 59.598, -151.828},
	{"46211", "Grays Harbor", "WA", 46.857, -124.243},
	{"46213", "Cape Mendocino", "CA", 40.291, -124.748},
	{"46214", "Point Reyes", "CA", 37.944, -123.466},
	{"46236", "Monterey Canyon Outer", "CA", 36.759, -121.95},
	{"46243", "Clatsop Spit", "OR", 46.215, -124.129},
	{"46248", "Astoria Canyon", "OR", 46.133, -124.64},
	{"46267", "Angeles Point", "WA", 48.171, -123.6},
	{"51000", "Northern Hawaii One", "HI", 23.534, -153.752},
	{"51001", "Northwestern Hawaii One", "HI", 24.475, -162.03},
	{"51002", "Southwest Hawaii", "HI", 17.07, -157.755},
	{"51004", "Southeast Hawaii", "HI", 17.504, -152.197},
	{"51201", "Waimea Bay", "HI", 21.671, -158.118},
	{"51202", "Mokapu Point", "HI", 21.414, -157.681},
	{"51205", "Pauwela, Maui", "HI", 21.018, -156.421},
	{"51206", "Hilo, Hawaii", "HI", 19.779, -154.97},
	{"51211", "Pearl Harbor Entrance", "HI", 21.297, -157.959},
	{"51212", "Barbers Point, Kalaeloa", "HI", 21.323, -158.149},
}

// catalogEntry renders this buoy as a searchable catalog row: the place it
// reports from, with the region it belongs to.
func (s buoyStation) catalogEntry() catalogEntry {
	label := s.Name
	if s.Region != "" {
		label += ", " + s.Region
	}
	return catalogEntry{
		ID:     s.ID,
		Label:  label,
		Region: s.Region,
		Point:  LatLng{Lat: s.Lat, Lng: s.Lng},
	}
}

// buoyCatalog is the reference table in the uniform form the discovery commands
// search. It is built once from the same table the buoy command validates an id
// against, so the lookup and the discovery can never disagree.
var buoyCatalog = catalogFrom(buoyStations, buoyStation.catalogEntry)
