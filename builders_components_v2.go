package discordgo

import "slices"

// NewTextDisplay creates a Components V2 markdown text element.
func NewTextDisplay(content string) TextDisplay {
	return TextDisplay{Content: content}
}

// SectionBuilder groups text displays with a button or thumbnail accessory.
type SectionBuilder struct {
	section Section
}

// NewSection starts a Components V2 section with the given accessory.
// The accessory must be a Button or Thumbnail.
func NewSection(accessory MessageComponent) *SectionBuilder {
	return &SectionBuilder{section: Section{Accessory: accessory}}
}

// AddText appends a markdown text display to the section.
func (b *SectionBuilder) AddText(content string) *SectionBuilder {
	b.section.Components = append(b.section.Components, NewTextDisplay(content))
	return b
}

// Build returns the constructed section.
func (b *SectionBuilder) Build() Section {
	section := b.section
	section.Components = slices.Clone(section.Components)
	return section
}

// ContainerBuilder groups Components V2 elements in a visually distinct block.
type ContainerBuilder struct {
	container Container
}

// NewContainer starts a Components V2 container.
func NewContainer() *ContainerBuilder { return &ContainerBuilder{} }

// AccentColor sets the color of the container's accent bar.
func (b *ContainerBuilder) AccentColor(color int) *ContainerBuilder {
	b.container.AccentColor = &color
	return b
}

// Spoiler sets whether the container starts hidden.
func (b *ContainerBuilder) Spoiler(spoiler bool) *ContainerBuilder {
	b.container.Spoiler = spoiler
	return b
}

// AddComponent appends an element to the container.
func (b *ContainerBuilder) AddComponent(component MessageComponent) *ContainerBuilder {
	b.container.Components = append(b.container.Components, component)
	return b
}

// Build returns the constructed container.
func (b *ContainerBuilder) Build() Container {
	container := b.container
	container.Components = slices.Clone(container.Components)
	return container
}

// MediaGalleryBuilder constructs a gallery of images, video, or GIFs.
type MediaGalleryBuilder struct {
	gallery MediaGallery
}

// NewMediaGallery starts a Components V2 media gallery.
func NewMediaGallery() *MediaGalleryBuilder { return &MediaGalleryBuilder{} }

// AddItem appends an item to the gallery.
func (b *MediaGalleryBuilder) AddItem(item MediaGalleryItem) *MediaGalleryBuilder {
	b.gallery.Items = append(b.gallery.Items, item)
	return b
}

// Build returns the constructed gallery.
func (b *MediaGalleryBuilder) Build() MediaGallery {
	gallery := b.gallery
	gallery.Items = slices.Clone(gallery.Items)
	return gallery
}
