package utilitiesServiceInterfaces

type AddTemplateRequest struct {
	Name string `json:"name" binding:"required,min=1,max=255"`
	User string `json:"user" binding:"required"`
	Meta string `json:"meta" binding:"required"`
}

type EditTemplateRequest struct {
	ID   uint   `json:"-"`
	Name string `json:"name" binding:"omitempty,min=1,max=255"`
	User string `json:"user"`
	Meta string `json:"meta"`
}
