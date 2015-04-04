package framework

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
	"text/template"

	"github.com/hashicorp/vault/logical"
)

// Path is a single path that the backend responds to.
type Path struct {
	// Pattern is the pattern of the URL that matches this path.
	//
	// This should be a valid regular expression. Named captures will be
	// exposed as fields that should map to a schema in Fields. If a named
	// capture is not a field in the Fields map, then it will be ignored.
	Pattern string

	// Fields is the mapping of data fields to a schema describing that
	// field. Named captures in the Pattern also map to fields. If a named
	// capture name matches a PUT body name, the named capture takes
	// priority.
	//
	// Note that only named capture fields are available in every operation,
	// whereas all fields are avaiable in the Write operation.
	Fields map[string]*FieldSchema

	// Callbacks are the set of callbacks that are called for a given
	// operation. If a callback for a specific operation is not present,
	// then logical.ErrUnsupportedOperation is automatically generated.
	//
	// The help operation is the only operation that the Path will
	// automatically handle if the Help field is set. If both the Help
	// field is set and there is a callback registered here, then the
	// callback will be called.
	Callbacks map[logical.Operation]OperationFunc

	// Help is text describing how to use this path. This will be used
	// to auto-generate the help operation. The Path will automatically
	// generate a parameter listing and URL structure based on the
	// regular expression, so the help text should just contain a description
	// of what happens.
	//
	// HelpSynopsis is a one-sentence description of the path. This will
	// be automatically line-wrapped at 80 characters.
	//
	// HelpDescription is a long-form description of the path. This will
	// be automatically line-wrapped at 80 characters.
	HelpSynopsis    string
	HelpDescription string
}

func (p *Path) helpCallback(
	req *logical.Request, data *FieldData) (*logical.Response, error) {
	var tplData pathTemplateData
	tplData.Request = req.Path
	tplData.RoutePattern = p.Pattern
	tplData.Synopsis = strings.TrimSpace(p.HelpSynopsis)
	tplData.Description = strings.TrimSpace(p.HelpDescription)

	// Alphabetize the fields
	fieldKeys := make([]string, 0, len(p.Fields))
	for k, _ := range p.Fields {
		fieldKeys = append(fieldKeys, k)
	}
	sort.Strings(fieldKeys)

	// Build the field help
	tplData.Fields = make([]pathTemplateFieldData, len(fieldKeys))
	for i, k := range fieldKeys {
		schema := p.Fields[k]
		description := strings.TrimSpace(schema.Description)
		if description == "" {
			description = "<no description>"
		}

		tplData.Fields[i] = pathTemplateFieldData{
			Key:         k,
			Type:        schema.Type.String(),
			Description: description,
		}
	}

	// Parse the help template
	tpl, err := template.New("root").Parse(pathHelpTemplate)
	if err != nil {
		return nil, fmt.Errorf("error parsing template: %s", err)
	}

	// Execute the template and store the output
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, &tplData); err != nil {
		return nil, fmt.Errorf("error executing template: %s", err)
	}

	return logical.HelpResponse(strings.TrimSpace(buf.String()), nil), nil
}

type pathTemplateData struct {
	Request      string
	RoutePattern string
	Synopsis     string
	Description  string
	Fields       []pathTemplateFieldData
}

type pathTemplateFieldData struct {
	Key         string
	Type        string
	Description string
	URL         bool
}

const pathHelpTemplate = `
Request:        {{.Request}}
Matching Route: {{.RoutePattern}}

{{.Synopsis}}

## Parameters
{{range .Fields}}
### {{.Key}} (type: {{.Type}})

{{.Description}}
{{end}}
## Description

{{.Description}}
`
