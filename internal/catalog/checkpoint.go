package catalog

import "errors"

func (c *Catalog) DeviceCheckpoint(device string) (SyncPosition, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	position, ok := c.checkpoints[device]
	if !ok {
		return SyncPosition{}, ErrSyncExpired
	}
	return position, c.validateSyncLocked(position)
}

func (c *Catalog) AcknowledgeDevice(device string, position SyncPosition) error {
	if device == "" || len(device) > 128 {
		return errors.New("invalid checkpoint device")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.validateSyncLocked(position); err != nil {
		return err
	}
	old, exists := c.checkpoints[device]
	if exists && old.Epoch == position.Epoch && old.Sequence > position.Sequence {
		return errors.New("checkpoint cannot move backwards")
	}
	if exists && old == position {
		return nil
	}
	if !exists && len(c.checkpoints) >= 128 {
		return errors.New("too many device checkpoints")
	}
	if c.checkpoints == nil {
		c.checkpoints = make(map[string]SyncPosition)
	}
	c.checkpoints[device] = position
	if err := c.saveFilesLocked(c.files); err != nil {
		if exists {
			c.checkpoints[device] = old
		} else {
			delete(c.checkpoints, device)
		}
		return err
	}
	return nil
}
