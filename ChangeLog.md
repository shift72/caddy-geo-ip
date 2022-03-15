# Change log

# 0.5.0
Refactored to support reloading without triggering multiple tickers
Add `override_country_code` to support development

# 0.4.0
Add env vars for `GEOIP_ACCOUNT_ID` and `GEOIP_API_KEY`

# 0.3.0
Support skipping GeoIP look up if the database has not been loaded. This helps
with the chicken and egg start up issues, where the database has not been loaded
but requests are being served (health checks potentially).
The Country Code returned is --

# 0.2.0
Renamed the variables from `geoip_country_code` to `geoip.country_code`

# 0.1.0
Initial release