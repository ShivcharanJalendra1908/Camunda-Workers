package blogpostgres

type Config struct {
	TaskType string `yaml:"task_type" mapstructure:"task_type"`
	Timeout  int    `yaml:"timeout" mapstructure:"timeout"`
}
