// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build js && wasm

package http

import (
	"errors"
	"fmt"
	"internal/godebug"
	"io"
	"net/http/internal/ascii"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"syscall/js"
)

var uint8Array = js.Global().Get("Uint8Array")

// jsFetchMode is a Request.Header map key that, if present,
// signals that the map entry is actually an option to the Fetch API mode setting.
// Valid values are: "cors", "no-cors", "same-origin", "navigate"
// The default is "same-origin".
//
// Reference: https://developer.mozilla.org/en-US/docs/Web/API/WindowOrWorkerGlobalScope/fetch#Parameters
const jsFetchMode = "js.fetch:mode"

// jsFetchCreds is a Request.Header map key that, if present,
// signals that the map entry is actually an option to the Fetch API credentials setting.
// Valid values are: "omit", "same-origin", "include"
// The default is "same-origin".
//
// Reference: https://developer.mozilla.org/en-US/docs/Web/API/WindowOrWorkerGlobalScope/fetch#Parameters
const jsFetchCreds = "js.fetch:credentials"

// jsFetchRedirect is a Request.Header map key that, if present,
// signals that the map entry is actually an option to the Fetch API redirect setting.
// Valid values are: "follow", "error", "manual"
// The default is "follow".
//
// Reference: https://developer.mozilla.org/en-US/docs/Web/API/WindowOrWorkerGlobalScope/fetch#Parameters
const jsFetchRedirect = "js.fetch:redirect"

// jsFetchMissing will be true if the Fetch API is not present in
// the browser globals.
var jsFetchMissing = js.Global().Get("fetch").IsUndefined()

// jsFetchNode is the jsfetchnode GODEBUG setting: jsfetchnode=1
// re-enables the Fetch API when running under Node.js. See
// jsFetchDisabled.
var jsFetchNode = godebug.New("jsfetchnode")

// jsIsNode reports whether the program appears to be running in Node.js.
var jsIsNode = js.Global().Get("process").Type() == js.TypeObject &&
	strings.HasPrefix(js.Global().Get("process").Get("argv0").String(), "node")

// jsFetchDisabled reports whether the use of the Fetch API is disabled.
// It's true when we detect we're running in Node.js, so that RoundTrip
// ends up talking over the same fake network the HTTP servers currently
// use in various tests and examples. See go.dev/issue/57613.
// The jsfetchnode GODEBUG setting re-enables the Fetch API under
// Node.js (18 or later, where fetch is a global), giving programs real
// HTTP support there; browsers are unaffected either way.
//
// TODO(go.dev/issue/60810): See if it's viable to test the Fetch API
// code path.
func jsFetchDisabled() bool {
	return jsIsNode && jsFetchNode.Value() != "1"
}

// RoundTrip implements the [RoundTripper] interface using the WHATWG Fetch API.
func (t *Transport) RoundTrip(req *Request) (*Response, error) {
	// The Transport has a documented contract that states that if the DialContext or
	// DialTLSContext functions are set, they will be used to set up the connections.
	// If they aren't set then the documented contract is to use Dial or DialTLS, even
	// though they are deprecated. Therefore, if any of these are set, we should obey
	// the contract and dial using the regular round-trip instead. Otherwise, we'll try
	// to fall back on the Fetch API, unless it's not available.
	if t.Dial != nil || t.DialContext != nil || t.DialTLS != nil || t.DialTLSContext != nil || jsFetchMissing || jsFetchDisabled() {
		return t.roundTrip(req)
	}

	ac := js.Global().Get("AbortController")
	if !ac.IsUndefined() {
		// Some browsers that support WASM don't necessarily support
		// the AbortController. See
		// https://developer.mozilla.org/en-US/docs/Web/API/AbortController#Browser_compatibility.
		ac = ac.New()
	}

	opt := js.Global().Get("Object").New()
	// See https://developer.mozilla.org/en-US/docs/Web/API/WindowOrWorkerGlobalScope/fetch
	// for options available.
	opt.Set("method", req.Method)
	opt.Set("credentials", "same-origin")
	if h := req.Header.Get(jsFetchCreds); h != "" {
		opt.Set("credentials", h)
		req.Header.Del(jsFetchCreds)
	}
	if h := req.Header.Get(jsFetchMode); h != "" {
		opt.Set("mode", h)
		req.Header.Del(jsFetchMode)
	}
	if h := req.Header.Get(jsFetchRedirect); h != "" {
		opt.Set("redirect", h)
		req.Header.Del(jsFetchRedirect)
	}
	if !ac.IsUndefined() {
		opt.Set("signal", ac.Get("signal"))
	}
	headers := js.Global().Get("Headers").New()
	for key, values := range req.Header {
		for _, value := range values {
			headers.Call("append", key, value)
		}
	}
	opt.Set("headers", headers)

	// closeRequestBody closes req.Body exactly once: RoundTrip must
	// always close the body, including on errors. The streaming upload
	// path below replaces it; the buffered path closes the body itself
	// and leaves this a no-op.
	closeRequestBody := func() {}
	if req.Body != nil {
		if req.outgoingLength() < 0 && supportsRequestStreams() {
			// The body length is unknown (ContentLength < 0, or 0 with
			// a non-nil Body, per outgoingLength — exactly the requests
			// the HTTP/1 transport would send chunked), and this
			// runtime's fetch supports streaming request bodies: upload
			// the body incrementally instead of buffering it.
			//
			// Known-length bodies deliberately keep the buffered path
			// below: fetch drops Content-Length for ReadableStream
			// bodies, so buffering is what keeps Content-Length on the
			// wire exactly as before. Note that fetch cannot replay a
			// stream body, so a redirect that re-sends the body becomes
			// a network error on this path (the HTTP/1 transport cannot
			// replay such a body either, absent GetBody).
			stream, closeBody := fetchRequestStream(req.Body)
			closeRequestBody = closeBody
			opt.Set("duplex", "half")
			opt.Set("body", stream)
		} else {
			// TODO(johanbrandhorst): Stream request body when possible.
			// See https://bugs.chromium.org/p/chromium/issues/detail?id=688906 for Blink issue.
			// See https://bugzilla.mozilla.org/show_bug.cgi?id=1387483 for Firefox issue.
			// See https://github.com/web-platform-tests/wpt/issues/7693 for WHATWG tests issue.
			// See https://developer.mozilla.org/en-US/docs/Web/API/Streams_API for more details on the Streams API
			// and browser support.
			// NOTE(haruyama480): Ensure HTTP/1 fallback exists.
			// See https://go.dev/issue/61889 for discussion.
			body, err := io.ReadAll(req.Body)
			if err != nil {
				req.Body.Close() // RoundTrip must always close the body, including on errors.
				return nil, err
			}
			req.Body.Close()
			if len(body) != 0 {
				buf := uint8Array.New(len(body))
				js.CopyBytesToJS(buf, body)
				opt.Set("body", buf)
			}
		}
	}

	fetchPromise := js.Global().Call("fetch", req.URL.String(), opt)
	var (
		respCh           = make(chan *Response, 1)
		errCh            = make(chan error, 1)
		success, failure js.Func
	)
	success = js.FuncOf(func(this js.Value, args []js.Value) any {
		success.Release()
		failure.Release()

		result := args[0]
		header := Header{}
		// https://developer.mozilla.org/en-US/docs/Web/API/Headers/entries
		headersIt := result.Get("headers").Call("entries")
		for {
			n := headersIt.Call("next")
			if n.Get("done").Bool() {
				break
			}
			pair := n.Get("value")
			key, value := pair.Index(0).String(), pair.Index(1).String()
			ck := CanonicalHeaderKey(key)
			header[ck] = append(header[ck], value)
		}

		contentLength := int64(0)
		clHeader := header.Get("Content-Length")
		switch {
		case clHeader != "":
			cl, err := strconv.ParseInt(clHeader, 10, 64)
			if err != nil {
				errCh <- fmt.Errorf("net/http: ill-formed Content-Length header: %v", err)
				return nil
			}
			if cl < 0 {
				// Content-Length values less than 0 are invalid.
				// See: https://datatracker.ietf.org/doc/html/rfc2616/#section-14.13
				errCh <- fmt.Errorf("net/http: invalid Content-Length header: %q", clHeader)
				return nil
			}
			contentLength = cl
		default:
			// If the response length is not declared, set it to -1.
			contentLength = -1
		}

		b := result.Get("body")
		var body io.ReadCloser
		// The body is undefined when the browser does not support streaming response bodies (Firefox),
		// and null in certain error cases, i.e. when the request is blocked because of CORS settings.
		if !b.IsUndefined() && !b.IsNull() {
			body = &streamReader{stream: b.Call("getReader")}
		} else {
			// Fall back to using ArrayBuffer
			// https://developer.mozilla.org/en-US/docs/Web/API/Body/arrayBuffer
			body = &arrayReader{arrayPromise: result.Call("arrayBuffer")}
		}

		code := result.Get("status").Int()

		uncompressed := false
		if ascii.EqualFold(header.Get("Content-Encoding"), "gzip") {
			// The fetch api will decode the gzip, but Content-Encoding not be deleted.
			header.Del("Content-Encoding")
			header.Del("Content-Length")
			contentLength = -1
			uncompressed = true
		}

		if result.Get("redirected").Bool() {
			u, err := url.Parse(result.Get("url").String())
			if err == nil {
				req = req.Clone(req.ctx)
				req.URL = u
			}
		}
		respCh <- &Response{
			Status:        fmt.Sprintf("%d %s", code, StatusText(code)),
			StatusCode:    code,
			Header:        header,
			ContentLength: contentLength,
			Uncompressed:  uncompressed,
			Body:          body,
			Request:       req,
		}

		return nil
	})
	failure = js.FuncOf(func(this js.Value, args []js.Value) any {
		success.Release()
		failure.Release()

		err := args[0]
		// The error is a JS Error type
		// https://developer.mozilla.org/en-US/docs/Web/JavaScript/Reference/Global_Objects/Error
		// We can use the toString() method to get a string representation of the error.
		errMsg := err.Call("toString").String()
		// Errors can optionally contain a cause.
		if cause := err.Get("cause"); !cause.IsUndefined() {
			// The exact type of the cause is not defined,
			// but if it's another error, we can call toString() on it too.
			if !cause.Get("toString").IsUndefined() {
				errMsg += ": " + cause.Call("toString").String()
			} else if cause.Type() == js.TypeString {
				errMsg += ": " + cause.String()
			}
		}
		errCh <- fmt.Errorf("net/http: fetch() failed: %s", errMsg)
		return nil
	})

	fetchPromise.Call("then", success, failure)
	select {
	case <-req.Context().Done():
		if !ac.IsUndefined() {
			// Abort the Fetch request.
			ac.Call("abort")

			// Wait for fetch promise to be rejected prior to exiting. See
			// https://github.com/golang/go/issues/57098 for more details.
			select {
			case resp := <-respCh:
				resp.Body.Close()
			case <-errCh:
			}
		}
		// Aborting normally cancels the request body stream, which
		// closes the body; this covers runtimes that don't, and the
		// no-AbortController case (where erroring the upload is also
		// the only way left to stop it).
		closeRequestBody()
		return nil, req.Context().Err()
	case resp := <-respCh:
		closeRequestBody()
		return resp, nil
	case err := <-errCh:
		closeRequestBody()
		return nil, err
	}
}

// supportsRequestStreams reports whether this runtime's fetch supports
// streaming request bodies (upload streaming), caching the result of a
// one-time feature probe: constructing a Request with a ReadableStream
// body must read the "duplex" option, and must not stringify the body —
// an environment without upload streaming stringifies the stream, which
// shows up as a text/plain Content-Type. Node.js 18+ and Chromium 105+
// pass. See
// https://developer.chrome.com/docs/capabilities/web-apis/fetch-streaming-requests.
var supportsRequestStreams = sync.OnceValue(func() (ok bool) {
	// Any surprise in the probe (missing constructors, a throwing
	// Request constructor, ...) means no streaming support.
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	global := js.Global()
	requestCtor := global.Get("Request")
	streamCtor := global.Get("ReadableStream")
	if requestCtor.Type() != js.TypeFunction || streamCtor.Type() != js.TypeFunction {
		return false
	}
	duplexAccessed := false
	get := js.FuncOf(func(this js.Value, args []js.Value) any {
		duplexAccessed = true
		return "half"
	})
	defer get.Release()
	object := global.Get("Object")
	opts := object.New()
	opts.Set("method", "POST")
	opts.Set("body", streamCtor.New())
	desc := object.New()
	desc.Set("get", get)
	object.Call("defineProperty", opts, "duplex", desc)
	// The URL is never fetched; it only has to parse. (The browser
	// version of this probe uses "", which does not parse under
	// Node.js: there is no base URL there.)
	request := requestCtor.New("https://go.dev/", opts)
	return duplexAccessed && !request.Get("headers").Call("has", "Content-Type").Bool()
})

// fetchRequestStream wraps body in a JavaScript ReadableStream for use
// as a streaming fetch request body (with the "half" duplex option).
// Each pull hands one Read's worth of bytes to the stream from a fresh
// goroutine — the pull callback runs on the event loop and must not
// block — and resolves the pull's promise afterwards, so pulls are
// serialized and backpressure propagates to the reader.
//
// The returned closeBody function closes body exactly once. It is
// called here when the upload completes (io.EOF), fails, or is
// cancelled by the runtime (e.g. on abort); the caller must also call
// it on every RoundTrip exit path so the RoundTripper contract — the
// body is always closed — holds even if the runtime never consumes or
// cancels the stream.
func fetchRequestStream(body io.ReadCloser) (stream js.Value, closeBody func()) {
	closeBody = sync.OnceFunc(func() { body.Close() })

	// The stream can be cancelled or errored at any moment (an abort
	// races an in-flight read), after which controller operations
	// throw. Those exceptions carry no information we act on: swallow
	// them instead of panicking.
	call := func(v js.Value, method string, args ...any) {
		defer func() { recover() }()
		v.Call(method, args...)
	}

	var pull, cancel js.Func
	release := sync.OnceFunc(func() {
		pull.Release()
		cancel.Release()
	})

	buf := make([]byte, 64<<10) // safe to reuse: pulls never overlap
	pull = js.FuncOf(func(this js.Value, args []js.Value) any {
		controller := args[0]
		var resolve js.Value
		executor := js.FuncOf(func(this js.Value, args []js.Value) any {
			resolve = args[0]
			return nil
		})
		// The Promise constructor invokes the executor before New
		// returns, so resolve is set here and executor can be released.
		promise := js.Global().Get("Promise").New(executor)
		executor.Release()
		go func() {
			for {
				n, err := body.Read(buf)
				if n > 0 {
					chunk := uint8Array.New(n)
					js.CopyBytesToJS(chunk, buf[:n])
					call(controller, "enqueue", chunk)
				}
				switch {
				case err == io.EOF:
					closeBody()
					call(controller, "close")
					release()
				case err != nil:
					closeBody()
					call(controller, "error", js.Global().Get("Error").New(err.Error()))
					release()
				case n == 0:
					// Read returned (0, nil); retry here rather than
					// resolving an empty pull, which would just have
					// the stream call pull again immediately.
					continue
				}
				resolve.Invoke()
				return
			}
		}()
		return promise
	})
	cancel = js.FuncOf(func(this js.Value, args []js.Value) any {
		// The runtime is done with the stream (the fetch was aborted or
		// failed): no further pulls will happen. Closing the body also
		// unblocks any in-flight read.
		closeBody()
		release()
		return nil
	})

	source := js.Global().Get("Object").New()
	source.Set("pull", pull)
	source.Set("cancel", cancel)
	return js.Global().Get("ReadableStream").New(source), closeBody
}

var errClosed = errors.New("net/http: reader is closed")

// streamReader implements an io.ReadCloser wrapper for ReadableStream.
// See https://fetch.spec.whatwg.org/#readablestream for more information.
type streamReader struct {
	pending []byte
	stream  js.Value
	err     error // sticky read error
}

func (r *streamReader) Read(p []byte) (n int, err error) {
	if r.err != nil {
		return 0, r.err
	}
	if len(r.pending) == 0 {
		var (
			bCh   = make(chan []byte, 1)
			errCh = make(chan error, 1)
		)
		success := js.FuncOf(func(this js.Value, args []js.Value) any {
			result := args[0]
			if result.Get("done").Bool() {
				errCh <- io.EOF
				return nil
			}
			value := make([]byte, result.Get("value").Get("byteLength").Int())
			js.CopyBytesToGo(value, result.Get("value"))
			bCh <- value
			return nil
		})
		defer success.Release()
		failure := js.FuncOf(func(this js.Value, args []js.Value) any {
			// Assumes it's a TypeError. See
			// https://developer.mozilla.org/en-US/docs/Web/JavaScript/Reference/Global_Objects/TypeError
			// for more information on this type. See
			// https://streams.spec.whatwg.org/#byob-reader-read for the spec on
			// the read method.
			errCh <- errors.New(args[0].Get("message").String())
			return nil
		})
		defer failure.Release()
		r.stream.Call("read").Call("then", success, failure)
		select {
		case b := <-bCh:
			r.pending = b
		case err := <-errCh:
			r.err = err
			return 0, err
		}
	}
	n = copy(p, r.pending)
	r.pending = r.pending[n:]
	return n, nil
}

func (r *streamReader) Close() error {
	// This ignores any error returned from cancel method. So far, I did not encounter any concrete
	// situation where reporting the error is meaningful. Most users ignore error from resp.Body.Close().
	// If there's a need to report error here, it can be implemented and tested when that need comes up.
	r.stream.Call("cancel")
	if r.err == nil {
		r.err = errClosed
	}
	return nil
}

// arrayReader implements an io.ReadCloser wrapper for ArrayBuffer.
// https://developer.mozilla.org/en-US/docs/Web/API/Body/arrayBuffer.
type arrayReader struct {
	arrayPromise js.Value
	pending      []byte
	read         bool
	err          error // sticky read error
}

func (r *arrayReader) Read(p []byte) (n int, err error) {
	if r.err != nil {
		return 0, r.err
	}
	if !r.read {
		r.read = true
		var (
			bCh   = make(chan []byte, 1)
			errCh = make(chan error, 1)
		)
		success := js.FuncOf(func(this js.Value, args []js.Value) any {
			// Wrap the input ArrayBuffer with a Uint8Array
			uint8arrayWrapper := uint8Array.New(args[0])
			value := make([]byte, uint8arrayWrapper.Get("byteLength").Int())
			js.CopyBytesToGo(value, uint8arrayWrapper)
			bCh <- value
			return nil
		})
		defer success.Release()
		failure := js.FuncOf(func(this js.Value, args []js.Value) any {
			// Assumes it's a TypeError. See
			// https://developer.mozilla.org/en-US/docs/Web/JavaScript/Reference/Global_Objects/TypeError
			// for more information on this type.
			// See https://fetch.spec.whatwg.org/#concept-body-consume-body for reasons this might error.
			errCh <- errors.New(args[0].Get("message").String())
			return nil
		})
		defer failure.Release()
		r.arrayPromise.Call("then", success, failure)
		select {
		case b := <-bCh:
			r.pending = b
		case err := <-errCh:
			return 0, err
		}
	}
	if len(r.pending) == 0 {
		return 0, io.EOF
	}
	n = copy(p, r.pending)
	r.pending = r.pending[n:]
	return n, nil
}

func (r *arrayReader) Close() error {
	if r.err == nil {
		r.err = errClosed
	}
	return nil
}
