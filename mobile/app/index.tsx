// v0.3.0: placeholder route. The orb screen lands in TASK-019.
import { StyleSheet, Text, View } from 'react-native';

export default function FridayPlaceholder() {
  return (
    <View style={styles.container}>
      <Text style={styles.text}>Friday — pairing pending</Text>
    </View>
  );
}

const styles = StyleSheet.create({
  container: {
    flex: 1,
    alignItems: 'center',
    justifyContent: 'center',
    backgroundColor: '#0a0e1a',
  },
  text: {
    color: '#22d3ee',
    fontFamily: 'Menlo',
    fontSize: 16,
  },
});
