package mcpserver

type obj = map[string]any

func prop(typ, desc string) obj { return obj{"type": typ, "description": desc} }

func genProps() obj {
	return obj{
		"prompt":       prop("string", "Text prompt describing the image."),
		"provider":     prop("string", "Provider instance name (see list_providers). Default gemini."),
		"model":        prop("string", "Model ID or 'latest' (see list_models)."),
		"size":         prop("string", "Resolution or size, e.g. 1024x1024 or 2K."),
		"aspect_ratio": prop("string", "Aspect ratio, e.g. 16:9."),
		"quality":      prop("string", "Quality level, provider specific."),
		"format":       prop("string", "Output format: png, jpeg or webp."),
		"background":   prop("string", "Background: transparent, opaque or auto."),
		"n":            prop("integer", "Number of images to generate."),
		"output_dir":   prop("string", "Directory to write images to."),
		"name":         prop("string", "Output file base name."),
		"seed":         prop("integer", "Seed for reproducible output, when the provider supports it."),
		"remove_bg":    prop("boolean", "Remove a flat background after generation to give a transparent PNG."),
		"resize":       prop("string", "Post-process resize, e.g. 512x512."),
		"denoise":      prop("string", "Reduce grain and speckle before other post-processing: light, medium or strong."),
		"extra": obj{
			"type":                 "object",
			"description":          "Provider-specific parameters as key/value pairs.",
			"additionalProperties": true,
		},
	}
}

func tools() []obj {
	gen := genProps()
	edit := genProps()
	edit["images"] = obj{
		"type":        "array",
		"description": "Paths of the input images to edit.",
		"items":       obj{"type": "string"},
		"minItems":    1,
	}
	edit["mask"] = prop("string", "Path of a mask image, when the provider supports masks.")
	return []obj{
		{
			"name":        "generate_image",
			"description": "Generate images from a text prompt. Returns file paths, model, cost, and the images inline when small.",
			"inputSchema": obj{"type": "object", "properties": gen, "required": []string{"prompt"}},
		},
		{
			"name":        "edit_image",
			"description": "Edit one or more existing images with a text prompt. Returns file paths, model, cost, and the images inline when small.",
			"inputSchema": obj{"type": "object", "properties": edit, "required": []string{"prompt", "images"}},
		},
		{
			"name":        "list_models",
			"description": "List the models a provider offers.",
			"inputSchema": obj{"type": "object", "properties": obj{"provider": prop("string", "Provider instance name. Default gemini.")}},
		},
		{
			"name":        "list_providers",
			"description": "List the configured providers.",
			"inputSchema": obj{"type": "object", "properties": obj{}},
		},
	}
}
