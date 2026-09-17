package recvignore

type BaseModel struct{}

type Service struct{}

// Methods of BaseModel are ignored, so this must not be reported.
func modelHelper() {}

func (b *BaseModel) Save() {
	modelHelper()
}

func serviceHelper() { // want "serviceHelper is called only from methods of \\*Service; consider making it an unexported method"
}

func (s *Service) Run() {
	serviceHelper()
}
