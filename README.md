# moonbeam

moonbeam is a tool to generate API client code from OpenAPI specification.

## Install

```bash
go install github.com/aide-family/moonbeam@latest
```

## Generate

```bash
moonbeam -f openapi.yaml -o ./api
```

## Usage

```yaml
# openapi.yaml
openapi: 3.0.0
info:
  title: Test API
  version: 1.0.0
paths:
  /test:
    get:
      operationId: test_get
      tags: [test]
      summary: Test endpoint
      responses:
        '200':
          description: OK
components:
  schemas:
    TestResponse:
      type: object
      properties:
        message:
          type: string
```

```bash
moonbeam -f openapi.yaml -o ./api
```

## Output

```bash
tree -L 2 -a ./api
```