# CountryInfo – Cloud Assignment 1

Test

This project is part of Cloud Technologies assignment 1.

The service is written in Go and exposes a REST API that combines data from two upstream services:

- RestCountries API (country data)
- Currency API (exchange rates)

The API provides three main endpoints:
- Service status
- Country information
- Exchange rates for neighboring countries

---

## How to run locally

Make sure you have Go 1.22 or newer versions installed.

Start the server:

```bash
go run main.go
```

The service will run on:

http://localhost:8080

---

## API Endpoints

### 1. Service Status

GET  
/countryinfo/v1/status/

Returns:
- Status code of both upstream services
- API version
- Service uptime (in seconds)

---

### 2. Country Information

GET  
/countryinfo/v1/info/{countryCode}

Example:
http://localhost:8080/countryinfo/v1/info/no

Returns:
- Country name
- Continents
- Population
- Area
- Languages
- Borders
- Flag (PNG)
- Capital

Only ISO alpha-2 country codes are supported.

---

### 3. Exchange Rates

GET  
/countryinfo/v1/exchange/{countryCode}

Example:
http://localhost:8080/countryinfo/v1/exchange/no

Returns:
- Country name
- Base currency
- Exchange rates to neighboring countries’ currencies

If the country has no neighbors, an empty exchange-rates object is returned.

---

## Error Handling

The API returns meaningful HTTP status codes:

- 200 – Success
- 400 – Invalid or missing country code
- 404 – Country not found
- 502 – Upstream service error
- 503 – Upstream service unavailable (status endpoint)
- 504 – Upstream timeout

Errors are returned in JSON format:

```json
{
  "error": "description"
}
```

---

## Environment Variables

The following environment variables can be configured:

- PORT (default: 8080)
- RESTCOUNTRIES_BASE_URL
- CURRENCY_BASE_URL

The service uses the PORT variable automatically when deployed to cloud platforms such as Render.

---

## Technical Notes

- Uses context with timeout for all upstream calls
- Limits concurrency when fetching neighbor country data
- Handles upstream failures gracefully
- Returns structured JSON responses