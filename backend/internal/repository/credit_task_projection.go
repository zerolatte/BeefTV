package repository

import "infinite-canvas/backend/internal/model"

// 批量读取账本终态，任务列表与详情使用相同的结果取回判定。
func (r *Repository) CreditReservationsForTasks(ids []string) (map[string]model.CreditReservation, error) {
	result := make(map[string]model.CreditReservation, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	var rows []model.CreditReservation
	if err := r.db.Where("task_id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		result[row.TaskID] = row
	}
	return result, nil
}
