#!/bin/sh

swag init -g internal/handlers/routes.go --parseDependency --parseInternal -q --output docs/swagger
