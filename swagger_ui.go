package webservice

import (
	"embed"
	"html/template"
	"maps"
	"net/http"
	"net/url"
	"slices"

	"github.com/labstack/echo/v4"
	"github.com/vearutop/statigz"
)

//go:generate sh internal/swaggerui/update.sh 5.35.1
//go:embed internal/swaggerui/dist
var swaggerAssets embed.FS

var swaggerIndex = template.Must(template.New("swagger").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Swagger UI</title>
  <base href="{{.Base}}">
  <link rel="icon" href="favicon-32x32.png">
  <link rel="stylesheet" href="swagger-ui.css">
</head>
<body style="margin:0">
  <div id="swagger-ui"></div>
  <script src="swagger-ui-bundle.js"></script>
  <script src="swagger-ui-standalone-preset.js"></script>
  <script>
    window.ui = SwaggerUIBundle({
      dom_id: "#swagger-ui",
      urls: {{.URLs}},
      oauth2RedirectUrl: new URL("oauth2-redirect.html", document.baseURI).href,
      "urls.primaryName": new URLSearchParams(location.search).get("urls.primaryName"),
      deepLinking: true,
      validatorUrl: null,
      presets: [SwaggerUIBundle.presets.apis, SwaggerUIStandalonePreset],
      plugins: [SwaggerUIBundle.plugins.DownloadUrl],
      layout: "StandaloneLayout"
    });
  </script>
</body>
</html>`))

func (b *APIBuilder) registerSwaggerUIRoutes() {
	type specURL struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	}
	urls := make([]specURL, 0, len(b.specifications))
	for _, name := range slices.Sorted(maps.Keys(b.specifications)) {
		urls = append(urls, specURL{Name: name, URL: "./" + (&url.URL{Path: name}).EscapedPath()})
	}
	index := func(c echo.Context) error {
		c.Response().Header().Set(echo.HeaderContentType, echo.MIMETextHTMLCharsetUTF8)
		base := "./"
		if c.Path() == "/swagger" {
			base = "./swagger/"
		}
		return swaggerIndex.Execute(c.Response(), struct {
			Base string
			URLs []specURL
		}{base, urls})
	}
	b.echo.GET("/swagger", index)
	b.echo.GET("/swagger/", index)
	b.echo.GET("/swagger/index.html", index)
	assets := statigz.FileServer(swaggerAssets, statigz.FSPrefix("internal/swaggerui/dist"))
	b.echo.GET("/swagger/*", echo.WrapHandler(http.StripPrefix("/swagger/", assets)))
}
