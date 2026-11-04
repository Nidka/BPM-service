package application

import "time"

type Service struct {
	work UnitOfWork
	now  func() time.Time
}

func New(work UnitOfWork) *Service { return &Service{work: work, now: time.Now} }
