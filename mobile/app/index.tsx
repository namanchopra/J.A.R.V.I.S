// v0.3.0: orb root route. TASK-019 replaces the placeholder Text with the
// real <OrbView /> centrepiece. State stays at 'idle' / sessions=11 until
// the WS client (TASK-023) wires the daemon's state_change + pipeline_status
// events through to props.
import { OrbView } from '../components/OrbView';

export default function FridayRoot() {
  return <OrbView state="idle" sessions={11} />;
}
