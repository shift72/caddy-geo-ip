# Change log


# 0.3.0
Support skipping GeoIP look up if the database has not been loaded. This helps
with the chicken and egg start up issues, where the database has not been loaded
but requests are being served (health checks potentially).
The Country Code returned is --

# 0.2.0
Renamed the variables from `geoip_country_code` to `geoip.country_code`

# 0.1.0
Initial release