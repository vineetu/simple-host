package db

import (
	"github.com/vsriram/simple-host/internal/model"
	repo "github.com/vsriram/simple-host/internal/repository/hackentries"
)

type VotingSettings = model.VotingSettings
type VoteOption = model.VoteOption

var GetVotingSettings = repo.GetVotingSettings
var SaveVotingSettings = repo.SaveVotingSettings
var SetDirectoryListed = repo.SetDirectoryListed
var VoteOptions = repo.VoteOptions
var VoteFinalRanking = repo.VoteFinalRanking
var OwnVote = repo.OwnVote
var VoterEligible = repo.VoterEligible
var IneligibleVotes = repo.IneligibleVotes
