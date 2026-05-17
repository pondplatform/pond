module github.com/pondplatform/pond/agent

go 1.25.5

require (
	github.com/google/uuid v1.6.0
	github.com/gorilla/websocket v1.5.3
	github.com/pondplatform/pond/shared v0.0.0-00010101000000-000000000000
)

replace github.com/pondplatform/pond/shared => ../shared
