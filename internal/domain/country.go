package domain

// Country is one Natural Earth admin-0 unit. ISO3 = ADM0_A3 (never ISO_A3: it is -99 for FRA/NOR).
type Country struct {
	ISO3, ISO2, Name, Continent string
	Lon, Lat                    float64 // label point
}
