package systemServiceInterfaces

type DevdEvent struct {
	System    string
	Subsystem string
	Type      string
	Attrs     map[string]string
	Raw       string
}
