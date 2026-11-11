package discordgo

// Fluent builders for message components (actions rows, buttons, select
// menus) and modals. They construct the raw structs from components.go and
// interactions.go — no new wire types. Example:
//
//	resp := discordgo.NewMessage().
//		Content("Pick an option:").
//		AddRow(discordgo.NewRow().
//			AddButton(discordgo.NewPrimaryButton("accept", "role-accept")).
//			AddButton(discordgo.NewDangerButton("deny", "role-deny"))).
//		Build()

// RowBuilder fluently constructs an ActionsRow. A row holds at most five
// interactive components (buttons / select menus).
type RowBuilder struct {
	row ActionsRow
}

// NewRow returns an empty RowBuilder.
func NewRow() *RowBuilder {
	return &RowBuilder{}
}

// AddButton appends a button built with a ButtonBuilder.
func (b *RowBuilder) AddButton(button *ButtonBuilder) *RowBuilder {
	button_ := button.Build()
	b.row.Components = append(b.row.Components, &button_)
	return b
}

// AddButtonRaw appends a raw Button component.
func (b *RowBuilder) AddButtonRaw(button Button) *RowBuilder {
	b.row.Components = append(b.row.Components, &button)
	return b
}

// AddMenu appends a select menu built with a SelectMenuBuilder.
func (b *RowBuilder) AddMenu(menu *SelectMenuBuilder) *RowBuilder {
	menu_ := menu.Build()
	b.row.Components = append(b.row.Components, &menu_)
	return b
}

// AddMenuRaw appends a raw SelectMenu component.
func (b *RowBuilder) AddMenuRaw(menu SelectMenu) *RowBuilder {
	b.row.Components = append(b.row.Components, &menu)
	return b
}

// AddRaw appends any raw MessageComponent (TextInput for modals, or the
// newer Components V2 layout types).
func (b *RowBuilder) AddRaw(component MessageComponent) *RowBuilder {
	b.row.Components = append(b.row.Components, component)
	return b
}

// Build returns the constructed ActionsRow. The builder remains reusable.
func (b *RowBuilder) Build() ActionsRow {
	row := b.row
	return row
}

// ButtonBuilder fluently constructs a Button. Link buttons take a URL and
// no CustomID; all other styles take a CustomID and no URL.
type ButtonBuilder struct {
	button Button
}

// NewButton returns an empty ButtonBuilder (defaults to
// PrimaryButton style when marshalled).
func NewButton() *ButtonBuilder {
	return &ButtonBuilder{}
}

// NewPrimaryButton returns a blurple button with the given label and
// custom ID.
func NewPrimaryButton(label, customID string) *ButtonBuilder {
	return NewButton().Label(label).CustomID(customID)
}

// NewSecondaryButton returns a grey button with the given label and
// custom ID.
func NewSecondaryButton(label, customID string) *ButtonBuilder {
	return NewButton().Label(label).Style(SecondaryButton).CustomID(customID)
}

// NewSuccessButton returns a green button with the given label and
// custom ID.
func NewSuccessButton(label, customID string) *ButtonBuilder {
	return NewButton().Label(label).Style(SuccessButton).CustomID(customID)
}

// NewDangerButton returns a red button with the given label and
// custom ID.
func NewDangerButton(label, customID string) *ButtonBuilder {
	return NewButton().Label(label).Style(DangerButton).CustomID(customID)
}

// NewLinkButton returns a link button with the given label and URL. Link
// buttons must not have a CustomID.
func NewLinkButton(label, url string) *ButtonBuilder {
	return NewButton().Label(label).Style(LinkButton).URL(url)
}

// Label sets the button label text.
func (b *ButtonBuilder) Label(label string) *ButtonBuilder {
	b.button.Label = label
	return b
}

// Style sets the button style (PrimaryButton, SecondaryButton,
// SuccessButton, DangerButton, LinkButton, PremiumButton).
func (b *ButtonBuilder) Style(style ButtonStyle) *ButtonBuilder {
	b.button.Style = style
	return b
}

// CustomID sets the developer-defined identifier sent back with the
// MESSAGE_COMPONENT interaction.
func (b *ButtonBuilder) CustomID(customID string) *ButtonBuilder {
	b.button.CustomID = customID
	return b
}

// URL sets the navigation URL; only for LinkButton style.
func (b *ButtonBuilder) URL(url string) *ButtonBuilder {
	b.button.URL = url
	return b
}

// Emoji sets the button emoji.
func (b *ButtonBuilder) Emoji(emoji *ComponentEmoji) *ButtonBuilder {
	b.button.Emoji = emoji
	return b
}

// EmojiName sets the button emoji by its unicode name (e.g. "🎉").
func (b *ButtonBuilder) EmojiName(name string) *ButtonBuilder {
	b.button.Emoji = &ComponentEmoji{Name: name}
	return b
}

// Disabled greys the button out so it cannot be clicked.
func (b *ButtonBuilder) Disabled() *ButtonBuilder {
	b.button.Disabled = true
	return b
}

// ID sets the component identifier used by Discord's auto-population
// accounting.
func (b *ButtonBuilder) ID(id int) *ButtonBuilder {
	b.button.ID = id
	return b
}

// Build returns the constructed Button. The builder remains reusable.
func (b *ButtonBuilder) Build() Button {
	button := b.button
	return button
}

// SelectMenuBuilder fluently constructs a SelectMenu (string, user, role,
// channel or mentionable). String menus take Options; the auto-populated
// types (user/role/channel/mentionable) must not.
type SelectMenuBuilder struct {
	menu SelectMenu
}

// NewSelectMenu returns a string select menu builder with the given custom
// ID. Options are added with Option / OptionDesc.
func NewSelectMenu(customID string) *SelectMenuBuilder {
	return &SelectMenuBuilder{menu: SelectMenu{CustomID: customID, MenuType: StringSelectMenu}}
}

// NewUserSelectMenu returns an auto-populated user select menu builder.
func NewUserSelectMenu(customID string) *SelectMenuBuilder {
	return &SelectMenuBuilder{menu: SelectMenu{CustomID: customID, MenuType: UserSelectMenu}}
}

// NewRoleSelectMenu returns an auto-populated role select menu builder.
func NewRoleSelectMenu(customID string) *SelectMenuBuilder {
	return &SelectMenuBuilder{menu: SelectMenu{CustomID: customID, MenuType: RoleSelectMenu}}
}

// NewChannelSelectMenu returns an auto-populated channel select menu
// builder.
func NewChannelSelectMenu(customID string) *SelectMenuBuilder {
	return &SelectMenuBuilder{menu: SelectMenu{CustomID: customID, MenuType: ChannelSelectMenu}}
}

// NewMentionableSelectMenu returns an auto-populated mentionable select
// menu builder.
func NewMentionableSelectMenu(customID string) *SelectMenuBuilder {
	return &SelectMenuBuilder{menu: SelectMenu{CustomID: customID, MenuType: MentionableSelectMenu}}
}

// Placeholder sets the text shown when nothing is selected.
func (b *SelectMenuBuilder) Placeholder(placeholder string) *SelectMenuBuilder {
	b.menu.Placeholder = placeholder
	return b
}

// Option appends a select option with a label and a value.
func (b *SelectMenuBuilder) Option(label, value string) *SelectMenuBuilder {
	b.menu.Options = append(b.menu.Options, SelectMenuOption{Label: label, Value: value})
	return b
}

// OptionDesc appends a select option with a label, value and description.
func (b *SelectMenuBuilder) OptionDesc(label, value, description string) *SelectMenuBuilder {
	b.menu.Options = append(b.menu.Options, SelectMenuOption{Label: label, Value: value, Description: description})
	return b
}

// OptionDefault appends a select option pre-selected by default.
func (b *SelectMenuBuilder) OptionDefault(label, value, description string) *SelectMenuBuilder {
	b.menu.Options = append(b.menu.Options, SelectMenuOption{Label: label, Value: value, Description: description, Default: true})
	return b
}

// MinValues sets the minimum number of items the user must select.
func (b *SelectMenuBuilder) MinValues(n int) *SelectMenuBuilder {
	b.menu.MinValues = &n
	return b
}

// MaxValues sets the maximum number of items the user can select; values
// above 1 make the menu multi-select.
func (b *SelectMenuBuilder) MaxValues(n int) *SelectMenuBuilder {
	b.menu.MaxValues = n
	return b
}

// Disabled greys the menu out so it cannot be interacted with.
func (b *SelectMenuBuilder) Disabled() *SelectMenuBuilder {
	b.menu.Disabled = true
	return b
}

// Required marks the menu required (modals only).
func (b *SelectMenuBuilder) Required(required bool) *SelectMenuBuilder {
	b.menu.Required = &required
	return b
}

// ChannelTypes restricts which channel types appear (channel select menus
// only).
func (b *SelectMenuBuilder) ChannelTypes(types ...ChannelType) *SelectMenuBuilder {
	b.menu.ChannelTypes = append(b.menu.ChannelTypes, types...)
	return b
}

// DefaultValues pre-selects entities for auto-populated (user/role/channel/
// mentionable) menus.
func (b *SelectMenuBuilder) DefaultValues(values ...SelectMenuDefaultValue) *SelectMenuBuilder {
	b.menu.DefaultValues = append(b.menu.DefaultValues, values...)
	return b
}

// Build returns the constructed SelectMenu. The builder remains reusable.
func (b *SelectMenuBuilder) Build() SelectMenu {
	menu := b.menu
	return menu
}

// TextInputBuilder fluently constructs a TextInput for modals.
type TextInputBuilder struct {
	input TextInput
}

// NewTextInput returns a TextInput builder with the given custom ID. The
// value submitted with the modal is delivered under this ID.
func NewTextInput(customID string) *TextInputBuilder {
	return &TextInputBuilder{input: TextInput{CustomID: customID}}
}

// Style sets the input style: TextInputShort (single line) or
// TextInputParagraph (multi line).
func (b *TextInputBuilder) Style(style TextInputStyle) *TextInputBuilder {
	b.input.Style = style
	return b
}

// Placeholder sets the placeholder text.
func (b *TextInputBuilder) Placeholder(placeholder string) *TextInputBuilder {
	b.input.Placeholder = placeholder
	return b
}

// Value pre-fills the input.
func (b *TextInputBuilder) Value(value string) *TextInputBuilder {
	b.input.Value = value
	return b
}

// Required makes the input mandatory.
func (b *TextInputBuilder) Required(required bool) *TextInputBuilder {
	b.input.Required = &required
	return b
}

// MinLength sets the minimum submitted length (0-4000).
func (b *TextInputBuilder) MinLength(n int) *TextInputBuilder {
	b.input.MinLength = n
	return b
}

// MaxLength sets the maximum submitted length (1-4000).
func (b *TextInputBuilder) MaxLength(n int) *TextInputBuilder {
	b.input.MaxLength = n
	return b
}

// Build returns the constructed TextInput. The builder remains reusable.
func (b *TextInputBuilder) Build() TextInput {
	input := b.input
	return input
}

// LabelBuilder fluently constructs a Label, the required wrapper for
// components inside modals.
type LabelBuilder struct {
	label Label
}

// NewLabel returns a Label builder with the given label text.
func NewLabel(text string) *LabelBuilder {
	return &LabelBuilder{label: Label{Label: text}}
}

// Description sets the label description (optional).
func (b *LabelBuilder) Description(description string) *LabelBuilder {
	b.label.Description = description
	return b
}

// TextInput wraps the given input builder as the label's component.
func (b *LabelBuilder) TextInput(input *TextInputBuilder) *LabelBuilder {
	input_ := input.Build()
	b.label.Component = &input_
	return b
}

// TextInputRaw wraps a raw TextInput as the label's component.
func (b *LabelBuilder) TextInputRaw(input TextInput) *LabelBuilder {
	b.label.Component = &input
	return b
}

// RawComponent wraps any raw MessageComponent (e.g. SelectMenu,
// FileUpload) as the label's component.
func (b *LabelBuilder) RawComponent(component MessageComponent) *LabelBuilder {
	b.label.Component = component
	return b
}

// Build returns the constructed Label. The builder remains reusable.
func (b *LabelBuilder) Build() Label {
	label := b.label
	return label
}

// ModalBuilder fluently constructs a modal response
// (InteractionResponseModal). Example:
//
//	s.ReplyModal(i, discordgo.NewModal("feedback", "Send feedback").
//		AddLabel(discordgo.NewLabel("Your feedback").
//			Description("What can we improve?").
//			TextInput(discordgo.NewTextInput("feedback_text").
//				Style(discordgo.TextInputParagraph).
//				Required(true))))
type ModalBuilder struct {
	customID string
	title    string
	entries  []MessageComponent
}

// NewModal returns a ModalBuilder with the given custom ID and title.
// The submitted modal is delivered as an InteractionModalSubmit
// interaction carrying this custom ID.
func NewModal(customID, title string) *ModalBuilder {
	return &ModalBuilder{customID: customID, title: title}
}

// AddLabel appends a labelled component built with a LabelBuilder. A modal
// takes at most five top-level components.
func (b *ModalBuilder) AddLabel(label *LabelBuilder) *ModalBuilder {
	label_ := label.Build()
	b.entries = append(b.entries, &label_)
	return b
}

// AddRaw appends any raw top-level component (power users; legacy modals
// also accepted bare TextInput entries inside an ActionsRow).
func (b *ModalBuilder) AddRaw(component MessageComponent) *ModalBuilder {
	b.entries = append(b.entries, component)
	return b
}

// Build returns the constructed modal interaction response, ready for
// InteractionRespond or ReplyModal. The builder remains reusable.
func (b *ModalBuilder) Build() *InteractionResponse {
	return &InteractionResponse{
		Type: InteractionResponseModal,
		Data: &InteractionResponseData{
			CustomID:   b.customID,
			Title:      b.title,
			Components: b.entries,
		},
	}
}
