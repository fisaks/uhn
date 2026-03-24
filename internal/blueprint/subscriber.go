package blueprint

import "context"

// identitySubscriber adapts BlueprintDownloader.HandleMasterIdentity to the messaging.Subscriber interface.
type identitySubscriber struct {
	downloader *BlueprintDownloader
}

func (s *identitySubscriber) OnMessage(ctx context.Context, topic string, payload []byte, _ bool) {
	s.downloader.HandleMasterIdentity(payload)
}

// IdentitySubscriber returns a messaging.Subscriber that handles master identity messages.
func (d *BlueprintDownloader) IdentitySubscriber() *identitySubscriber {
	return &identitySubscriber{downloader: d}
}

// blueprintSubscriber adapts BlueprintDownloader.HandleBlueprintActivated to the messaging.Subscriber interface.
type blueprintSubscriber struct {
	downloader *BlueprintDownloader
}

func (s *blueprintSubscriber) OnMessage(ctx context.Context, topic string, payload []byte, _ bool) {
	s.downloader.HandleBlueprintActivated(payload)
}

// BlueprintSubscriber returns a messaging.Subscriber that handles blueprint activated messages.
func (d *BlueprintDownloader) BlueprintSubscriber() *blueprintSubscriber {
	return &blueprintSubscriber{downloader: d}
}
