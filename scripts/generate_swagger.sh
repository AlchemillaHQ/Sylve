#!/bin/sh

swag init -g internal/handlers/routes.go --parseDependency --parseInternal -q --outputTypes json,yaml --output docs/swagger
