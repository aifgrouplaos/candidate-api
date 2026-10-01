// Package apidocs serves the OpenAPI contract and a Swagger UI page for trying it.
package apidocs

import (
	_ "embed"

	"github.com/gofiber/fiber/v2"
)

//go:embed openapi.yaml
var contract []byte

// Swagger UI is pinned with SRI hashes because the page runs on the API origin
// and persists the bearer token; bump version and hashes together.
const page = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>Candidate API docs</title>
<link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5.33.0/swagger-ui.css" integrity="sha384-Ov4/wv3j2bmct8cDc5X4ngJZohVPzEmc6uDPH8WeljUxO5vtoykvMEfbu9Vh6RaW" crossorigin="anonymous">
</head>
<body>
<div id="swagger-ui"></div>
<script src="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5.33.0/swagger-ui-bundle.js" integrity="sha384-YDALVcy8kj8yltLBVi1vBiBAUqdxvus673gM8XKwiy6aDUJFXivF/KCufekjYbVf" crossorigin="anonymous"></script>
<script>SwaggerUIBundle({ url: "/openapi.yaml", dom_id: "#swagger-ui", persistAuthorization: true });</script>
</body>
</html>`

// Register mounts GET /docs (Swagger UI) and GET /openapi.yaml (raw contract).
func Register(router fiber.Router) {
	router.Get("/docs", func(c *fiber.Ctx) error {
		return c.Type("html").SendString(page)
	})
	router.Get("/openapi.yaml", func(c *fiber.Ctx) error {
		c.Set(fiber.HeaderContentType, "application/yaml")
		return c.Send(contract)
	})
}
