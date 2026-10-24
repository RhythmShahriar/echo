Response shapes of Mailpit's API, written from its OpenAPI definitions
(server/ui/api/v1/swagger.json, October 2026: MessagesSummary,
MessageSummary, Message, Address). The parity tests compare field names
and JSON types, not values.

- messages.json: GET /api/v1/messages
- message.json: GET /api/v1/message/{ID}
