// v0.3.0: placeholder route. The orb screen lands in TASK-019.
//
// TASK-006: route through hud-tokens so the smoke verification path
// (Text styled with tokens.fontFamilies.mono + tokens.colors.cyan) is wired.
import { StyleSheet, Text, View } from 'react-native';

import { colors, fontFamilies, spacing } from '../lib/hud-tokens';

export default function FridayPlaceholder() {
  return (
    <View style={styles.container}>
      <Text style={styles.heading}>JARVIS</Text>
      <Text style={styles.subtext}>Friday — pairing pending</Text>
    </View>
  );
}

const styles = StyleSheet.create({
  container: {
    flex: 1,
    alignItems: 'center',
    justifyContent: 'center',
    backgroundColor: colors.bg,
    gap: spacing.md,
  },
  heading: {
    color: colors.cyan,
    fontFamily: fontFamilies.monoBold,
    fontSize: 24,
    letterSpacing: 4,
  },
  subtext: {
    color: colors.textDim,
    fontFamily: fontFamilies.mono,
    fontSize: 14,
  },
});
