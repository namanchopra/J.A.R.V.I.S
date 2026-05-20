// v0.3.0: orb root route. TASK-021 replaces the raw OrbView with the
// PushToTalkButton wrapper so the orb itself is the press-and-hold control.
// The WS client (TASK-023) will later wire onAudioChunk/onPressStart/
// onPressEnd through to the daemon; for now the button captures audio and
// drops it on the floor, which still exercises the listening-state visual.
//
// TASK-029: overlay a small gear button in the top-right corner so the
// user can reach the Settings screen (re-pair, ping test, host info)
// without leaving the orb-first UX. The gear sits above the orb in
// z-order via absolute positioning + a Pressable that doesn't intercept
// the underlying PushToTalkButton's hit area (gear is ~32x32, the orb
// fills the rest of the screen).
import { Pressable, StyleSheet, Text, View } from 'react-native';
import { useRouter } from 'expo-router';

import { PushToTalkButton } from '../components/PushToTalkButton';
import { colors, fontFamilies } from '../lib/hud-tokens';

export default function FridayRoot() {
  const router = useRouter();
  return (
    <View style={styles.root}>
      <PushToTalkButton sessions={11} />
      <Pressable
        onPress={() => router.push('/settings')}
        style={styles.gear}
        // Soft hit-slop so the gear is comfortably tappable without
        // covering more of the orb's listening hit area.
        hitSlop={8}
        testID="orb-settings-gear"
        accessibilityLabel="Open settings"
        accessibilityRole="button"
      >
        {/* Unicode gear glyph -- avoids pulling in an icon library for a
            single 14px element. Color matches HUD cyan at 30% opacity so
            it reads as a chrome control, not as part of the orb. */}
        <Text style={styles.gearText}>⚙</Text>
      </Pressable>
    </View>
  );
}

const styles = StyleSheet.create({
  root: {
    flex: 1,
    backgroundColor: colors.bg,
  },
  gear: {
    position: 'absolute',
    top: 20,
    right: 20,
    width: 32,
    height: 32,
    justifyContent: 'center',
    alignItems: 'center',
    // High z-index so the gear sits above the orb's Pressable. RN's
    // zIndex isn't strictly required when the gear is rendered after
    // the orb (paint order), but explicit is safer if the JSX is
    // reordered later.
    zIndex: 10,
  },
  gearText: {
    fontFamily: fontFamilies.mono,
    fontSize: 18,
    color: colors.cyan,
    opacity: 0.3,
  },
});
