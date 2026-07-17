import { API_URL, getAuthHeader, handleResponse, handleError } from './core';

// getMyBeamChannel - auth required. Returns:
//   { success, channel, effective_channel, dev_allowed }
// `channel` is the user's stored preference, `effective_channel` is it clamped by
// the current beam.dev_channel_access policy, and `dev_allowed` says whether the
// user may pick 'dev' right now.
export async function getMyBeamChannel() {
    try {
        const res = await fetch(`${API_URL}/me/beam-channel`, { headers: getAuthHeader() });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}

// setMyBeamChannel - auth required. channel is 'stable' | 'dev'. The server
// rejects 'dev' (403) unless the beam.dev_channel_access policy admits the caller,
// so the picker is only shown when dev_allowed is true.
export async function setMyBeamChannel(channel: 'stable' | 'dev') {
    try {
        const res = await fetch(`${API_URL}/me/beam-channel`, {
            method: 'PUT',
            headers: { ...getAuthHeader(), 'Content-Type': 'application/json' },
            body: JSON.stringify({ channel }),
        });
        return handleResponse(res);
    } catch (err) { return handleError(err); }
}
