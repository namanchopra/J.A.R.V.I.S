// v0.3.0: orb root route. TASK-021 replaces the raw OrbView with the
// PushToTalkButton wrapper so the orb itself is the press-and-hold control.
// The WS client (TASK-023) will later wire onAudioChunk/onPressStart/
// onPressEnd through to the daemon; for now the button captures audio and
// drops it on the floor, which still exercises the listening-state visual.
import { PushToTalkButton } from '../components/PushToTalkButton';

export default function FridayRoot() {
  return <PushToTalkButton sessions={11} />;
}
