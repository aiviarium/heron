package docs

// referenceParts puts the Reference area's parts in place: the API
// reference's groups as they are, then the schema reference, then the
// commands, each part under its heading where more than one has a page.
func (s *Site) referenceParts(docs, ref *Area) {
	api := ref.Groups
	schema := s.buildSchema(ref)
	cli := s.commandGroups(docs, ref)
	parts := 0
	for _, x := range [][]NavGroup{api, schema, cli} {
		if len(x) > 0 {
			parts++
		}
	}
	if parts > 1 {
		for i := range api {
			api[i].Part = label("docs.nav.part_api")
		}
	} else {
		for i := range schema {
			schema[i].Part = ""
		}
		for i := range cli {
			cli[i].Part = ""
		}
	}
	ref.Groups = append(append(api, schema...), cli...)
}

// commandGroups moves the pages of the tree's commands into the Reference
// area, each with its subcommands below it, in the order the tree writes
// them; the page that holds them stays where it is and links them.
func (s *Site) commandGroups(docs, ref *Area) []NavGroup {
	var tops []*Page
	for _, p := range s.Pages {
		if p.Node == nil || s.kindPath(p.Node) != "spec.command" {
			continue
		}
		if p.Parent != nil && p.Parent.Node != nil && s.kindPath(p.Parent.Node) == "spec.command" {
			continue
		}
		tops = append(tops, p)
	}
	if len(tops) == 0 {
		return nil
	}
	isTop := map[*Page]bool{}
	for _, p := range tops {
		isTop[p] = true
	}
	var drop func(ps []*Page) []*Page
	drop = func(ps []*Page) []*Page {
		var out []*Page
		for _, q := range ps {
			if isTop[q] {
				continue
			}
			q.Children = drop(q.Children)
			out = append(out, q)
		}
		return out
	}
	docs.Pages = drop(docs.Pages)
	var move func(p *Page)
	move = func(p *Page) {
		p.Area = ref
		for _, c := range p.Children {
			move(c)
		}
	}
	for _, p := range tops {
		move(p)
	}
	return []NavGroup{{Part: label("docs.nav.part_cli"), Pages: tops}}
}
