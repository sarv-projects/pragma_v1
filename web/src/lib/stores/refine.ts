import { writable } from 'svelte/store';

export const checkpointManifest = writable<Record<string, any>>({});
export const checkpointSpec = writable<Record<string, any>>({});
export const refinementMode = writable<boolean>(false);