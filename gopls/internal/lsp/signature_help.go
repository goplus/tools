// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package lsp

import (
	"context"

	"golang.org/x/tools/gopls/internal/lsp/protocol"
	"golang.org/x/tools/gopls/internal/lsp/source"
	"golang.org/x/tools/internal/event"
	"golang.org/x/tools/internal/event/tag"
)

func (s *Server) signatureHelp(ctx context.Context, params *protocol.SignatureHelpParams) (*protocol.SignatureHelp, error) {
	ctx, done := event.Start(ctx, "lsp.Server.signatureHelp", tag.URI.Of(params.TextDocument.URI))
	defer done()

	snapshot, fh, ok, release, err := s.beginFileRequest(ctx, params.TextDocument.URI, source.UnknownKind)
	defer release()
	if !ok {
		return nil, err
	}
	// goxls: overload
	var (
		infos           []protocol.SignatureInformation
		activeSignature int
		activeParameter int
	)
	switch kind := snapshot.View().FileKind(fh); kind {
	case source.Gop: // goxls: Go+
		infos, activeSignature, activeParameter, err = source.GopSignatureHelp(ctx, snapshot, fh, params.Position)
	case source.Go:
		var info *protocol.SignatureInformation
		info, activeParameter, err = source.SignatureHelp(ctx, snapshot, fh, params.Position)
		infos = []protocol.SignatureInformation{*info}
	default:
		return nil, nil
	}
	if err != nil {
		event.Error(ctx, "no signature help", err, tag.Position.Of(params.Position))
		return nil, nil // sic? There could be many reasons for failure.
	}
	return &protocol.SignatureHelp{
		Signatures:      infos,
		ActiveSignature: uint32(activeSignature),
		ActiveParameter: uint32(activeParameter),
	}, nil
}
