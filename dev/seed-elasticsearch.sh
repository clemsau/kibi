#!/usr/bin/env sh
set -eu

ES='http://elasticsearch-dev:9200'
AUTH='elastic:changeme-dev'

request() {
  curl --fail --silent --show-error -u "$AUTH" -H 'Content-Type: application/json' "$@"
}

echo 'Creating Kibi development sample indices…'

request -X PUT "$ES/products" -d '{
  "settings": {"number_of_shards": 1, "number_of_replicas": 0},
  "mappings": {"properties": {
    "name": {"type": "text", "fields": {"keyword": {"type": "keyword"}}},
    "category": {"type": "keyword"}, "price": {"type": "scaled_float", "scaling_factor": 100},
    "in_stock": {"type": "boolean"}, "tags": {"type": "keyword"}, "updated_at": {"type": "date"}
  }}
}' >/dev/null || true

request -X PUT "$ES/customers" -d '{
  "settings": {"number_of_shards": 1, "number_of_replicas": 0},
  "mappings": {"properties": {
    "name": {"type": "text"}, "email": {"type": "keyword"}, "tier": {"type": "keyword"},
    "joined_at": {"type": "date"}, "lifetime_value": {"type": "scaled_float", "scaling_factor": 100}
  }}
}' >/dev/null || true

request -X PUT "$ES/orders" -d '{
  "settings": {"number_of_shards": 1, "number_of_replicas": 0},
  "mappings": {"properties": {
    "customer_id": {"type": "keyword"}, "status": {"type": "keyword"}, "total": {"type": "scaled_float", "scaling_factor": 100},
    "items": {"type": "nested", "properties": {"sku": {"type": "keyword"}, "quantity": {"type": "integer"}}}, "created_at": {"type": "date"}
  }}
}' >/dev/null || true

request -X PUT "$ES/support_tickets" -d '{
  "settings": {"number_of_shards": 1, "number_of_replicas": 0},
  "mappings": {"properties": {
    "subject": {"type": "text"}, "priority": {"type": "keyword"}, "state": {"type": "keyword"},
    "customer_id": {"type": "keyword"}, "opened_at": {"type": "date"}
  }}
}' >/dev/null || true

request -X POST "$ES/_bulk?refresh=true" --data-binary @- <<'NDJSON' >/dev/null
{"index":{"_index":"products","_id":"kb-100"}}
{"name":"Kibi mechanical keyboard","category":"accessories","price":129.99,"in_stock":true,"tags":["keyboard","wireless","featured"],"updated_at":"2026-10-23T08:00:00Z"}
{"index":{"_index":"products","_id":"ms-200"}}
{"name":"Precision mouse","category":"accessories","price":79.50,"in_stock":true,"tags":["mouse","wireless"],"updated_at":"2026-10-22T14:30:00Z"}
{"index":{"_index":"products","_id":"mn-300"}}
{"name":"27 inch monitor","category":"displays","price":349.00,"in_stock":false,"tags":["monitor","4k"],"updated_at":"2026-10-18T10:00:00Z"}
{"index":{"_index":"customers","_id":"cus-001"}}
{"name":"Alice Martin","email":"alice@example.test","tier":"gold","joined_at":"2024-02-12","lifetime_value":1299.50}
{"index":{"_index":"customers","_id":"cus-002"}}
{"name":"Jon Bell","email":"jon@example.test","tier":"standard","joined_at":"2025-08-04","lifetime_value":379.00}
{"index":{"_index":"customers","_id":"cus-003"}}
{"name":"Mei Chen","email":"mei@example.test","tier":"gold","joined_at":"2023-11-18","lifetime_value":2675.90}
{"index":{"_index":"orders","_id":"ord-7001"}}
{"customer_id":"cus-001","status":"paid","total":209.49,"items":[{"sku":"kb-100","quantity":1},{"sku":"ms-200","quantity":1}],"created_at":"2026-10-20T09:15:00Z"}
{"index":{"_index":"orders","_id":"ord-7002"}}
{"customer_id":"cus-002","status":"shipped","total":349.00,"items":[{"sku":"mn-300","quantity":1}],"created_at":"2026-10-21T16:45:00Z"}
{"index":{"_index":"orders","_id":"ord-7003"}}
{"customer_id":"cus-003","status":"refunded","total":79.50,"items":[{"sku":"ms-200","quantity":1}],"created_at":"2026-10-22T11:20:00Z"}
{"index":{"_index":"support_tickets","_id":"ticket-41"}}
{"subject":"Keyboard pairing issue","priority":"high","state":"open","customer_id":"cus-001","opened_at":"2026-10-22T07:50:00Z"}
{"index":{"_index":"support_tickets","_id":"ticket-42"}}
{"subject":"Monitor delivery confirmation","priority":"normal","state":"pending","customer_id":"cus-002","opened_at":"2026-10-22T12:05:00Z"}
{"index":{"_index":"support_tickets","_id":"ticket-43"}}
{"subject":"Refund received","priority":"low","state":"closed","customer_id":"cus-003","opened_at":"2026-10-19T15:33:00Z"}
NDJSON

request -X POST "$ES/_aliases" -d '{"actions":[{"add":{"index":"products","alias":"catalogue_current"}}]}' >/dev/null
echo 'Kibi development sample data is ready.'
